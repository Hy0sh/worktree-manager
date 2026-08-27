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
	appReadyInterval = 0
	t.Cleanup(func() { appReadyInterval = time.Second })
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
	appReadyInterval = 0
	t.Cleanup(func() { appReadyInterval = time.Second })
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
	f := newFixture(t)
	appReadyInterval = 0
	t.Cleanup(func() { appReadyInterval = time.Second })
	misses := 31
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "/proc/net/tcp") && misses > 0 {
			misses--
			return execx.Result{ExitCode: 1}, errors.New("no match")
		}
		return inner(c)
	}
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(out.String(), "still waiting for backend to listen on 8000") {
		t.Fatalf("a long wait should keep saying so:\n%s", out.String())
	}
}
