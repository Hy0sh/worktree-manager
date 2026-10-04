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
	line := f.Lines()[0]
	for _, want := range []string{
		"-p myapp-wt-1-feat-x", "--project-directory /wt",
		"-f /wt/compose.yaml", "-f /wt/.wtm-snapshot.yaml", "up -d --build",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("up call %q should contain %q", line, want)
		}
	}
	if f.Calls[0].Dir != "/wt" {
		t.Fatalf("must run from the worktree, got %q", f.Calls[0].Dir)
	}
	if !slices.Contains(f.Calls[0].Env, "API_PORT=20087") {
		t.Fatalf("the port variables must reach compose's interpolation, got %q", f.Calls[0].Env)
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
