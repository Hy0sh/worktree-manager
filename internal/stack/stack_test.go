package stack

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func newClient(t *testing.T, f *execx.Fake) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	return &Client{Runner: f, Dir: dir, Out: io.Discard}, dir
}

// Up and Down now talk to docker compose directly, with the project name that
// isolates this worktree's containers, network and volumes.
func TestUpTargetsTheWorktreeProject(t *testing.T) {
	f := &execx.Fake{}
	c, _ := newClient(t, f)
	files := []string{"/wt/compose.yaml", "/wt/.wtm-snapshot.yaml"}
	if err := c.Up(context.Background(), "myapp-wt-1-feat-x", "/wt", files, []string{"API_PORT=20087"}, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}
	last := len(f.Calls) - 1
	line := f.Lines()[last]
	for _, want := range []string{
		"-p myapp-wt-1-feat-x", "--project-directory /wt",
		"-f /wt/compose.yaml", "-f /wt/.wtm-snapshot.yaml", "up -d --build",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("up call %q should contain %q", line, want)
		}
	}
	if f.Calls[last].Dir != "/wt" {
		t.Fatalf("must run from the worktree, got %q", f.Calls[last].Dir)
	}
	if !slices.Contains(f.Calls[last].Env, "API_PORT=20087") {
		t.Fatalf("the port variables must reach compose's interpolation, got %q", f.Calls[last].Env)
	}
}

// A volume shared across stacks is external: compose refuses to start without
// it and never creates it, so Up does, and only that one.
func TestUpCreatesTheExternalVolumes(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "config --format json") {
			return execx.Result{Stdout: `{"volumes":{
				"deps":{"name":"app-deps-1a2b3c","external":true},
				"db":{"name":"myapp-wt-1-feat-x_db"}}}`}, nil
		}
		return execx.Result{}, nil
	}}
	c, _ := newClient(t, f)
	if err := c.Up(context.Background(), "myapp-wt-1-feat-x", "/wt", nil, []string{"WTM_DEPS_KEY=1a2b3c"}, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !slices.Contains(f.Calls[0].Env, "WTM_DEPS_KEY=1a2b3c") {
		t.Fatalf("the config must interpolate like the up, got %q", f.Calls[0].Env)
	}
	var created []string
	for _, l := range f.Lines() {
		if after, ok := strings.CutPrefix(l, "docker volume create "); ok {
			created = append(created, after)
		}
	}
	if !slices.Equal(created, []string{"--label wtm.deps-key=1a2b3c app-deps-1a2b3c"}) {
		t.Fatalf("only the external volume should be created, got %q", created)
	}
}

// Only a volume named after the lockfile key is a dependency volume; the label
// is what a later cleanup finds it by, so nothing else may carry it.
func TestUpLabelsOnlyDependencyVolumes(t *testing.T) {
	cfg := `{"volumes":{
		"deps":{"name":"app-deps-1a2b3c","external":true},
		"cache":{"name":"shared-cache","external":true}}}`
	for name, tc := range map[string]struct {
		env  []string
		want []string
	}{
		"key in env": {[]string{"WTM_DEPS_KEY=1a2b3c"},
			[]string{"--label wtm.deps-key=1a2b3c app-deps-1a2b3c", "shared-cache"}},
		"no key": {nil, []string{"app-deps-1a2b3c", "shared-cache"}},
	} {
		f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
			if strings.Contains(c.String(), "config --format json") {
				return execx.Result{Stdout: cfg}, nil
			}
			return execx.Result{}, nil
		}}
		c, _ := newClient(t, f)
		if err := c.Up(context.Background(), "p", "/wt", nil, tc.env, nil); err != nil {
			t.Fatalf("%s: Up: %v", name, err)
		}
		var created []string
		for _, l := range f.Lines() {
			if after, ok := strings.CutPrefix(l, "docker volume create "); ok {
				created = append(created, after)
			}
		}
		slices.Sort(created)
		slices.Sort(tc.want)
		if !slices.Equal(created, tc.want) {
			t.Fatalf("%s: got %q, want %q", name, created, tc.want)
		}
	}
}

// A down between two starts left the anonymous volumes behind, which the next
// up never mounts again: stop keeps the containers, and with them the volumes.
func TestStopKeepsContainersAndVolumes(t *testing.T) {
	f := &execx.Fake{}
	c, _ := newClient(t, f)
	if err := c.Stop(context.Background(), "myapp-wt-1-feat-x", "/wt"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if line := f.Lines()[0]; !strings.HasSuffix(line, "compose -p myapp-wt-1-feat-x stop") {
		t.Fatalf("stop call = %q", line)
	}
}

func TestDownTakesTheVolumes(t *testing.T) {
	f := &execx.Fake{}
	c, _ := newClient(t, f)
	if err := c.Down(context.Background(), "myapp-wt-1-feat-x", "/wt"); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if line := f.Lines()[0]; !strings.HasSuffix(line, "compose -p myapp-wt-1-feat-x down --volumes") {
		t.Fatalf("down call = %q", line)
	}
}

// docker says a port is taken and names nobody. `docker ps` knows who publishes
// it, and that is the one line that turns the error into a fix.
func TestUpNamesTheContainerHoldingABusyPort(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "up -d"):
			return execx.Result{ExitCode: 1}, &execx.Error{Cmd: c.String(), ExitCode: 1, Live: true,
				Stderr: "Error response from daemon: driver failed programming external connectivity on endpoint x: Bind for 0.0.0.0:3306 failed: port is already allocated\n"}
		case strings.Contains(c.String(), "ps --format"):
			return execx.Result{Stdout: "my-app-tunnel-1\t0.0.0.0:3306->3306/tcp, 0.0.0.0:5434->5434/tcp\nother\t0.0.0.0:8080->80/tcp\n"}, nil
		}
		return execx.Result{}, nil
	}}
	c := &Client{Runner: f, Dir: "/repo"}
	err := c.Up(context.Background(), "p", "/repo/wt", nil, nil, nil)
	if err == nil {
		t.Fatal("expected the failure")
	}
	if !strings.Contains(err.Error(), "port 3306 is published by container my-app-tunnel-1") {
		t.Fatalf("name the holder, got %v", err)
	}
}

func TestUpLeavesOtherFailuresAlone(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "up -d") {
			return execx.Result{ExitCode: 1}, &execx.Error{Cmd: c.String(), ExitCode: 1, Stderr: "build failed\n"}
		}
		return execx.Result{}, nil
	}}
	c := &Client{Runner: f, Dir: "/repo"}
	err := c.Up(context.Background(), "p", "/repo/wt", nil, nil, nil)
	if err == nil || strings.Contains(err.Error(), "published by") {
		t.Fatalf("no port in that failure, got %v", err)
	}
	if got := lastLine(f); strings.Contains(got, "ps --format") {
		t.Fatalf("docker ps is only asked about a port, ran %q", got)
	}
}

func lastLine(f *execx.Fake) string {
	l := f.Lines()
	return l[len(l)-1]
}
