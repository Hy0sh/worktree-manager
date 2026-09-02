package worktree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
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

// A project's own script calling `docker compose` used to reach a stack named
// after the directory it ran from, which does not exist. The file list counts
// too: wtm's overrides are not named `override`, so compose ignores them.
func TestRunPointsComposeAtTheWorktreeStack(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.WorktreeIndices = map[string]int{"feat/x": 1}
	if err := Run(context.Background(), o, []string{"scripts/reset.sh"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	env := f.fake.Calls[len(f.fake.Calls)-1].Env
	want := "COMPOSE_PROJECT_NAME=" + stack.ProjectName(filepath.Base(f.root), 1, "feat/x")
	if !slices.Contains(env, want) {
		t.Fatalf("env = %v, want %q", env, want)
	}
	var files string
	for _, e := range env {
		if after, ok := strings.CutPrefix(e, "COMPOSE_FILE="); ok {
			files = after
		}
	}
	if !strings.HasSuffix(files, filepath.Join(".worktrees", "feat", "x", "compose.yaml")) {
		t.Fatalf("COMPOSE_FILE = %q, want the worktree's own compose file", files)
	}
}

// A repository without a compose file has no stack to point at, and `wtm run`
// is still the way to open an editor or an agent on the worktree.
func TestRunSetsNothingWithoutAStack(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.root, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	o := f.opts("feat/x")
	o.Project.WorktreeIndices = map[string]int{"feat/x": 1}
	if err := Run(context.Background(), o, []string{"claude"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if env := f.fake.Calls[len(f.fake.Calls)-1].Env; len(env) != 0 {
		t.Fatalf("env = %v, want none", env)
	}
}

// `create --run` hands the terminal to the command once everything else is done,
// with the compose environment `wtm run` would set. The index is the trap: the
// start that just happened allocated it, and this call's project copy lacks it.
func TestCreateRunsTheHostCommandWithTheComposeEnvironment(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.RunAfter = "claude"
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	last := f.fake.Calls[len(f.fake.Calls)-1]
	if last.Line() != `sh -c claude` {
		t.Fatalf("last call = %q, want the command played last", last.Line())
	}
	if want := filepath.Join(f.root, ".worktrees", "feat", "x"); last.Dir != want {
		t.Fatalf("working directory = %q, want the worktree %q", last.Dir, want)
	}
	want := "COMPOSE_PROJECT_NAME=" + stack.ProjectName(filepath.Base(f.root), 1, "feat/x")
	if !slices.Contains(last.Env, want) {
		t.Fatalf("env = %v, want %q", last.Env, want)
	}
}

// The worktree exists and works: losing it over a command that failed would be
// the worse outcome, so the failure is a warning naming the replay.
func TestCreateSurvivesAFailingHostCommand(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if c.Name == "sh" {
			return execx.Result{ExitCode: 127}, errors.New("claude: not found")
		}
		return inner(c)
	}
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.RunAfter = "claude --resume"
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create must not fail over --run: %v", err)
	}
	want := `wtm run feat/x -- sh -c 'claude --resume'`
	if !strings.Contains(out.String(), want) {
		t.Fatalf("the replay must be pastable, want %s in:\n%s", want, out.String())
	}
}

// Opening an agent on the files of a branch is a reason to create a worktree
// without paying for its stack.
func TestCreateRunsTheHostCommandWithoutAStack(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	o.RunAfter = "claude"
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	last := f.fake.Calls[len(f.fake.Calls)-1]
	if last.Line() != `sh -c claude` {
		t.Fatalf("last call = %q, want the command played all the same", last.Line())
	}
	if len(last.Env) != 0 {
		t.Fatalf("env = %v, want none: no stack was started", last.Env)
	}
}
