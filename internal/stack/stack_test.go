package stack

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func newClient(t *testing.T, f *execx.Fake) (*Client, string) {
	t.Helper()
	t.Setenv(config.EnvConfigDir, t.TempDir())
	dir := t.TempDir()
	return &Client{Runner: f, Dir: dir, Out: io.Discard}, dir
}

// dockerWith answers `config` with cfg, `volume inspect` as docker would for
// the existing volumes, and fail for the commands it names.
func dockerWith(cfg string, existing []string, fail string) *execx.Fake {
	return &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		switch {
		case fail != "" && strings.Contains(line, fail):
			return execx.Result{ExitCode: 1}, &execx.Error{Cmd: line, ExitCode: 1}
		case strings.Contains(line, "config --format json"):
			return execx.Result{Stdout: cfg}, nil
		case strings.Contains(line, "volume inspect") && !slices.Contains(existing, c.Args[len(c.Args)-1]):
			return execx.Result{ExitCode: 1}, &execx.Error{Cmd: line, ExitCode: 1}
		}
		return execx.Result{}, nil
	}}
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
	f := dockerWith(`{"volumes":{
		"deps":{"name":"app-deps-1a2b3c","external":true},
		"db":{"name":"myapp-wt-1-feat-x_db"}}}`, nil, "")
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
		f := dockerWith(cfg, nil, "")
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

const seedCfg = `{
	"services":{
		"api":{"volumes":[{"type":"volume","source":"deps","target":"/w/node_modules"}]},
		"web":{"volumes":[{"type":"volume","source":"deps","target":"/w/node_modules"}]}},
	"volumes":{"deps":{"name":"app-deps-1a2b3c","external":true}}}`

// Docker fills an empty volume on first mount, minutes for a node_modules, and
// a second stack mounting it meanwhile saw a partial tree: a volume wtm creates
// is filled by a throwaway container before any up.
func TestUpSeedsAFreshSharedVolumeBeforeTheUp(t *testing.T) {
	f := dockerWith(seedCfg, nil, "")
	c, _ := newClient(t, f)
	if err := c.Up(context.Background(), "p", "/wt", nil, nil, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var runs []string
	for _, l := range f.Lines() {
		if strings.Contains(l, " run ") {
			runs = append(runs, l)
		}
	}
	if len(runs) != 1 || !strings.HasSuffix(runs[0], "run --rm --no-deps --build -T --entrypoint true api") {
		t.Fatalf("one seeding run from the first service mounting it, got %q", runs)
	}
	if !strings.Contains(lastLine(f), "up -d --build") {
		t.Fatalf("the up comes after the seeding, last was %q", lastLine(f))
	}
}

func TestUpLeavesAnExistingSharedVolumeAlone(t *testing.T) {
	f := dockerWith(seedCfg, []string{"app-deps-1a2b3c"}, "")
	c, _ := newClient(t, f)
	if err := c.Up(context.Background(), "p", "/wt", nil, nil, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}
	for _, l := range f.Lines() {
		if strings.Contains(l, "volume create") || strings.Contains(l, " run ") {
			t.Fatalf("nothing to create nor seed, ran %q", l)
		}
	}
}

// A half-filled volume is never filled again by docker, so a failed seeding
// must not leave it for the next stack to mount.
func TestUpDropsAVolumeItFailedToSeed(t *testing.T) {
	f := dockerWith(seedCfg, nil, "--entrypoint true")
	c, _ := newClient(t, f)
	if err := c.Up(context.Background(), "p", "/wt", nil, nil, nil); err == nil {
		t.Fatal("expected the seeding failure")
	}
	last := f.Calls[len(f.Calls)-1]
	if last.Line() != "docker volume rm app-deps-1a2b3c" || last.Cancelled {
		t.Fatalf("the fresh volume must be dropped, last call %q", last.Line())
	}
}

// A wtm killed mid-copy leaves its pending file: the next up drops the
// half-filled volume and seeds it again instead of mounting it.
func TestUpReseedsAVolumeAnInterruptedSeedingLeft(t *testing.T) {
	f := dockerWith(seedCfg, []string{"app-deps-1a2b3c"}, "")
	c, _ := newClient(t, f)
	dir, _ := config.Dir()
	pending := filepath.Join(dir, "seeding", "app-deps-1a2b3c")
	if err := os.MkdirAll(filepath.Dir(pending), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Up(context.Background(), "p", "/wt", nil, nil, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}
	lines := strings.Join(f.Lines(), "\n")
	for _, want := range []string{"docker volume rm -f app-deps-1a2b3c", "volume create", "--entrypoint true api"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("expected %q among the calls:\n%s", want, lines)
		}
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatalf("a completed seeding must clear its pending file, stat: %v", err)
	}
}

// A volume still mounted by a running stack cannot be dropped: say so rather
// than seed over it.
func TestUpStopsOnAHalfFilledVolumeStillInUse(t *testing.T) {
	f := dockerWith(seedCfg, []string{"app-deps-1a2b3c"}, "volume rm -f")
	c, _ := newClient(t, f)
	dir, _ := config.Dir()
	if err := os.MkdirAll(filepath.Join(dir, "seeding"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seeding", "app-deps-1a2b3c"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := c.Up(context.Background(), "p", "/wt", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "stop the stacks mounting it") {
		t.Fatalf("expected the in-use error, got %v", err)
	}
	if slices.ContainsFunc(f.Lines(), func(l string) bool { return strings.Contains(l, "up -d") }) {
		t.Fatal("no up on a half-filled volume")
	}
}

// A second wtm waits for the first to finish seeding instead of mounting the
// volume halfway through.
func TestUpWaitsForAnotherSeeding(t *testing.T) {
	f := dockerWith(seedCfg, []string{"app-deps-1a2b3c"}, "")
	c, _ := newClient(t, f)
	dir, _ := config.Dir()
	held := flock.New(filepath.Join(dir, "volumes.lock"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if ok, err := held.TryLock(); !ok || err != nil {
		t.Fatalf("taking the lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Up(ctx, "p", "/wt", nil, nil, nil); err == nil {
		t.Fatal("Up must wait while another wtm holds the lock")
	}
	if slices.ContainsFunc(f.Lines(), func(l string) bool { return strings.Contains(l, "volume inspect") }) {
		t.Fatal("no volume may be looked at before the lock is taken")
	}
	_ = held.Unlock()
	if err := c.Up(context.Background(), "p", "/wt", nil, nil, nil); err != nil {
		t.Fatalf("Up after release: %v", err)
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
	env := []string{"WTM_DEPS_EXTERNAL=true"}
	if err := c.Down(context.Background(), "myapp-wt-1-feat-x", "/wt", env); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if line := f.Lines()[0]; !strings.HasSuffix(line, "compose -p myapp-wt-1-feat-x down --volumes") {
		t.Fatalf("down call = %q", line)
	}
	if got := f.Calls[0].Env; !slices.Equal(got, env) {
		t.Fatalf("down env = %v, want %v", got, env)
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
