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
	want := "docker compose -p " + project + " exec -T backend sh -c manage.py seed_data"
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
		if strings.Contains(c.String(), "exec -T backend sh -c") {
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

// Without a healthcheck there is nothing to wait on, and skipping the seed
// would be worse than running it early: say so and run it.
func TestCreateWarnsWhenTheAppHasNoHealthcheck(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(out.String(), "backend declares no healthcheck") {
		t.Fatalf("the warning should name the missing healthcheck:\n%s", out.String())
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.Contains(last, "exec -T backend sh -c") {
		t.Fatalf("post_create should still have run, last call = %q", last)
	}
}
