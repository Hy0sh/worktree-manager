package worktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// Running a command in a worktree stack otherwise means knowing the compose
// project name wtc derives, which is internal knowledge.
func TestExecTargetsTheWorktreeStack(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Exec(context.Background(), o, "", []string{"python", "manage.py", "seed_data"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	last := f.fake.Lines()[len(f.fake.Lines())-1]
	want := "docker compose -p " + stack.ProjectName(filepath.Base(f.root), 1, "feat/x") +
		" exec backend python manage.py seed_data"
	if last != want {
		t.Fatalf("exec call =\n  %q\nwant\n  %q", last, want)
	}
	if dir := f.fake.Calls[len(f.fake.Calls)-1].Dir; dir != filepath.Join(f.root, ".worktrees", "feat", "x") {
		t.Fatalf("must run from the worktree, got %q", dir)
	}
}

func TestExecServiceFlagOverridesTheConfiguredOne(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Exec(context.Background(), o, "frontend", []string{"sh"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.HasSuffix(last, "exec frontend sh") {
		t.Fatalf("--service should win, got %q", last)
	}
}

func TestExecWithoutAnyKnownServiceIsActionable(t *testing.T) {
	f := newFixture(t)
	err := Exec(context.Background(), f.opts("feat/x"), "", []string{"sh"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"--service", "app_service"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestPathReturnsTheWorktreeDirectory(t *testing.T) {
	f := newFixture(t)
	got, err := Path(context.Background(), f.opts("feat/x"))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := filepath.Join(f.root, ".worktrees", "feat", "x"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

// Run stays on the host, unlike Exec which enters the container.
func TestRunExecutesOnTheHostFromTheWorktree(t *testing.T) {
	f := newFixture(t)
	if err := Run(context.Background(), f.opts("feat/x"), []string{"claude", "--version"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := f.fake.Calls[len(f.fake.Calls)-1]
	if last.Name != "claude" || strings.Join(last.Args, " ") != "--version" {
		t.Fatalf("command = %q %v", last.Name, last.Args)
	}
	if want := filepath.Join(f.root, ".worktrees", "feat", "x"); last.Dir != want {
		t.Fatalf("working directory = %q, want the worktree %q", last.Dir, want)
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "docker") {
			t.Fatalf("Run must not go through docker, got %q", l)
		}
	}
}
