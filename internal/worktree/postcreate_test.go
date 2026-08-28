package worktree

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// The dump carries what the migrations create and never seed data, so a fresh
// worktree came up migrated and empty, and every developer seeded it by hand.
func TestCreatePlaysPostCreateInTheAppContainer(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Dump = true
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	project := stack.ProjectName(filepath.Base(f.root), 1, "feat/x")
	want := "docker compose -p " + project + " exec -T backend sh -c 'manage.py seed_data'"
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; last != want {
		t.Fatalf("last call =\n  %q\nwant\n  %q", last, want)
	}
}

func TestCreateWithoutPostCreateRunsNothingExtra(t *testing.T) {
	f := newFixture(t)
	if err := Create(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "exec -T") {
			t.Fatalf("nothing should have been played in a container, got %q", l)
		}
	}
}

// The worktree exists and works: losing it over a seed that did not run would
// be the worse outcome, so the failure is a warning naming the replay.
func TestCreateSurvivesAFailingPostCreate(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "sh -c 'manage.py") {
			return execx.Result{ExitCode: 1}, errors.New("seed_data: no such command")
		}
		return inner(c)
	}
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Project.PostCreate = "manage.py seed_data && manage.py create_users"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create must not fail over a seed: %v", err)
	}
	want := `wtm exec feat/x -- sh -c 'manage.py seed_data && manage.py create_users'`
	if !strings.Contains(out.String(), want) {
		t.Fatalf("the replay must be pastable, want %s in:\n%s", want, out.String())
	}
}

// Stacks install their dependencies from the service `command:` at boot, so a
// started container cannot run a management command yet.
func TestCreateWaitsForTheAppToBeHealthy(t *testing.T) {
	f := newFixture(t)
	health := []string{"starting", "starting", "healthy"}
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), ".Health") {
			out := health[0]
			if len(health) > 1 {
				health = health[1:]
			}
			return execx.Result{Stdout: out + "\n"}, nil
		}
		if strings.Contains(c.String(), "exec -T backend sh -c") && len(health) > 1 {
			t.Errorf("post_create ran while the app was %q", health[0])
		}
		return inner(c)
	}
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.Contains(last, "exec -T backend sh -c") {
		t.Fatalf("post_create should have run once healthy, last call = %q", last)
	}
	// Minutes of silence read as a hung wtm.
	if !strings.Contains(out.String(), "waiting for backend to report itself healthy") {
		t.Fatalf("the wait should say what it waits on:\n%s", out.String())
	}
}

// A queue worker publishes no port and declares no healthcheck, so there is
// nothing to wait on. Skipping the seed would be worse than running it early:
// say so and run it.
func TestCreateWarnsWhenNothingSaysTheAppIsReady(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.Backup = &config.Backup{AppService: "worker"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(out.String(), "worker declares no healthcheck and publishes no port") {
		t.Fatalf("the warning should name what is missing:\n%s", out.String())
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.Contains(last, "exec -T worker sh -c") {
		t.Fatalf("post_create should still have run, last call = %q", last)
	}
}

// The host side of a published port accepts a connection before anything
// listens inside the container, so readiness is read in the container's own
// network namespace.
func TestCreateWaitsForTheAppToListenWithoutAHealthcheck(t *testing.T) {
	f := newFixture(t)
	misses := 2
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "/proc/net/tcp") {
			// The container-side port of the fixture's backend, in hex.
			if !strings.Contains(c.String(), ":1F40 ") {
				t.Errorf("the probe should look for port 8000, got %q", c.String())
			}
			if misses > 0 {
				misses--
				return execx.Result{ExitCode: 1}, errors.New("no match")
			}
			return execx.Result{}, nil
		}
		if strings.Contains(c.String(), "sh -c 'manage.py") && misses > 0 {
			t.Error("post_create ran before the app listened")
		}
		return inner(c)
	}
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.Contains(last, "sh -c 'manage.py") {
		t.Fatalf("post_create should have run once listening, last call = %q", last)
	}
	if !strings.Contains(out.String(), "waiting for backend to listen on 8000") {
		t.Fatalf("the wait should name the port:\n%s", out.String())
	}
}

// The seed's own output scrolls the addresses out of sight, and they are what
// the developer came for.
func TestCreateRestatesTheAddressesAfterPostCreate(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	seed := strings.Index(out.String(), "post_create: manage.py seed_data")
	if seed < 0 {
		t.Fatalf("post_create should have run:\n%s", out.String())
	}
	after := out.String()[seed:]
	if !strings.Contains(after, "stack ready (worktree 1, feat/x)") || !strings.Contains(after, "backend  ") {
		t.Fatalf("the addresses should be restated after the seed:\n%s", after)
	}
}

// A wait of minutes with a single line at the top of it reads as a hung wtm.
func TestTheWaitKeepsSayingWhatItWaitsOn(t *testing.T) {
	var out bytes.Buffer
	o := Options{Out: &out}
	w := wait{timeout: 60 * time.Millisecond, interval: time.Millisecond, every: 10 * time.Millisecond}
	err := waitUntil(context.Background(), o, w, "backend to listen on 8000",
		" (it declares no healthcheck)", func() (bool, error) { return false, nil })
	if err == nil {
		t.Fatal("a wait that never succeeds must time out")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if lines[0] != "waiting for backend to listen on 8000 (it declares no healthcheck)" {
		t.Fatalf("first line = %q", lines[0])
	}
	if len(lines) < 2 {
		t.Fatalf("a wait of 60ms reminding every 10ms should repeat itself:\n%s", out.String())
	}
	// The reason belongs to that first line, not to every reminder.
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "still waiting for backend to listen on 8000 (") ||
			strings.Contains(l, "healthcheck") {
			t.Fatalf("reminder = %q", l)
		}
	}
}

// The elapsed time the wait reports used to be a count of attempts times the
// interval, which ignored what each probe itself costs. A `docker compose exec`
// on a machine busy booting nine services took about a second, so a wait that
// announced 2m0s had really been holding for 4m18s, and the ten minutes granted
// to an application service were over twenty in wall clock.
func TestTheWaitAccountsForWhatTheProbeItselfCosts(t *testing.T) {
	var out bytes.Buffer
	o := Options{Out: &out}
	w := wait{timeout: 100 * time.Millisecond, interval: time.Millisecond, every: time.Hour}
	start := time.Now()
	err := waitUntil(context.Background(), o, w, "backend to listen on 8000", "",
		func() (bool, error) {
			time.Sleep(10 * time.Millisecond)
			return false, nil
		})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a wait that never succeeds must time out")
	}
	// Counting attempts would have run 100 probes of 11ms, over a second.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("the wait held for %s, far past the 100ms it was given", elapsed)
	}
	if !strings.Contains(err.Error(), "backend to listen on 8000 after ") {
		t.Fatalf("the timeout should name what it waited on and for how long, got %v", err)
	}
}

// The bounds a project sets replace the built-in ones.
func TestReadyWaitTakesTheProjectBounds(t *testing.T) {
	if got := readyWait(config.Project{}, appReadyTimeout); got.timeout != appReadyTimeout ||
		got.interval != time.Second || got.every != stillWaiting {
		t.Fatalf("defaults = %+v", got)
	}
	p := config.Project{ReadyTimeout: "2m", ReadyInterval: "10s"}
	if got := readyWait(p, appReadyTimeout); got.timeout != 2*time.Minute || got.interval != 10*time.Second {
		t.Fatalf("2m every 10s = %+v", got)
	}
	// A duration the flags would have rejected must not shorten the wait.
	if got := readyWait(config.Project{ReadyTimeout: "nonsense"}, dbReadyTimeout); got.timeout != dbReadyTimeout {
		t.Fatalf("a malformed timeout should fall back, got %+v", got)
	}
	// Reminding more often than the wait even asks would say nothing new.
	if got := readyWait(config.Project{ReadyInterval: "1m"}, dbReadyTimeout); got.every != time.Minute {
		t.Fatalf("the reminder should not outpace the probe, got %+v", got)
	}
}

// A stack wanted for a quick look does not need the seed, and waiting on the
// application to be healthy is most of what a create spends its time on.
func TestCreateSkipsPostCreateWhenAsked(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.NoPostCreate = true
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "exec -T") || strings.Contains(l, ".Health") {
			t.Fatalf("nothing should have been played nor waited on, got %q", l)
		}
	}
	want := "post_create skipped (--no-post-create)"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("want %q in:\n%s", want, out.String())
	}
	if run := `wtm exec feat/x -- sh -c 'manage.py seed_data'`; !strings.Contains(out.String(), run) {
		t.Fatalf("the command must be pastable, want %s in:\n%s", run, out.String())
	}
}

// `create --exec` is a throwaway post_create: same container, same shell, and
// it comes after the project's own so a seed the branch depends on is there.
func TestCreatePlaysTheExecCommandAfterPostCreate(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.ExecAfter = "manage.py load_fixture demo"
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	project := stack.ProjectName(filepath.Base(f.root), 1, "feat/x")
	want := "docker compose -p " + project + " exec -T backend sh -c 'manage.py load_fixture demo'"
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; last != want {
		t.Fatalf("last call =\n  %q\nwant\n  %q", last, want)
	}
	seed := strings.Index(out.String(), "post_create: manage.py seed_data")
	if seed < 0 || !strings.Contains(out.String()[seed:], "exec: manage.py load_fixture demo") {
		t.Fatalf("--exec should come after post_create:\n%s", out.String())
	}
}

// The wait on the application belongs to whatever has to run in the container,
// not to post_create alone: a project without one still gets a cold container.
func TestCreateWaitsForTheAppBeforeTheExecCommand(t *testing.T) {
	f := newFixture(t)
	health := []string{"starting", "healthy"}
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), ".Health") {
			got := health[0]
			if len(health) > 1 {
				health = health[1:]
			}
			return execx.Result{Stdout: got + "\n"}, nil
		}
		if strings.Contains(c.String(), "sh -c 'manage.py") && len(health) > 1 {
			t.Errorf("--exec ran while the app was %q", health[0])
		}
		return inner(c)
	}
	o := f.opts("feat/x")
	o.ExecAfter = "manage.py load_fixture demo"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.Contains(last, "sh -c 'manage.py") {
		t.Fatalf("--exec should have run once healthy, last call = %q", last)
	}
}

// Same reasoning as a failing post_create, and --exec has its own replay line.
func TestCreateSurvivesAFailingExecCommand(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "sh -c 'manage.py load_fixture") {
			return execx.Result{ExitCode: 1}, errors.New("no fixture named demo")
		}
		return inner(c)
	}
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.ExecAfter = "manage.py load_fixture demo"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create must not fail over --exec: %v", err)
	}
	want := `wtm exec feat/x -- sh -c 'manage.py load_fixture demo'`
	if !strings.Contains(out.String(), want) {
		t.Fatalf("the replay must be pastable, want %s in:\n%s", want, out.String())
	}
}

// --no-post-create leaves the project's seed out, and says nothing about the
// command this create was asked to play itself.
func TestCreatePlaysTheExecCommandDespiteNoPostCreate(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.NoPostCreate = true
	o.ExecAfter = "manage.py load_fixture demo"
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.ReadyTimeout, o.Project.ReadyInterval = "1s", "1ms"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(out.String(), "post_create skipped (--no-post-create)") {
		t.Fatalf("the seed should have been skipped:\n%s", out.String())
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "sh -c 'manage.py seed_data'") {
			t.Fatalf("post_create should not have run, got %q", l)
		}
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.Contains(last, "sh -c 'manage.py load_fixture demo'") {
		t.Fatalf("--exec should still have run, last call = %q", last)
	}
}
