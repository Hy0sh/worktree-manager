package worktree

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
	o.Project.PostCreate = "manage.py seed_data"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create must not fail over a seed: %v", err)
	}
	if !strings.Contains(out.String(), "wtm exec feat/x -- manage.py seed_data") {
		t.Fatalf("the warning should name the replay:\n%s", out.String())
	}
}
