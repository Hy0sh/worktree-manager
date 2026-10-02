package worktree

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// adoptedFixture is an adopted worktree on worktree-curry whose fake git
// follows a `switch`, so the listing and the current directory answer the new
// branch afterwards. dirty and refuse script the two ways a switch stops.
func adoptedFixture(t *testing.T) (f *fixture, dirty *string, refuse *bool) {
	t.Helper()
	f, _ = foreignFixture(t)
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	f.managed = map[string]bool{"worktree-curry": true}
	dirty, refuse = new(string), new(bool)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		switch {
		case strings.Contains(line, "status --porcelain"):
			return execx.Result{Stdout: *dirty}, nil
		case c.Name == "git" && slices.Contains(c.Args, "switch"):
			if *refuse {
				return execx.Result{ExitCode: 128}, errors.New("already used by worktree")
			}
			to := c.Args[len(c.Args)-1]
			if i := slices.Index(c.Args, "-c"); i > 0 {
				to = c.Args[i+1]
			}
			f.foreign[to] = f.foreign[f.cwd]
			delete(f.foreign, f.cwd)
			f.cwd = to
			return execx.Result{}, nil
		}
		return inner(c)
	}
	return f, dirty, refuse
}

// switchOpts is what the command hands Switch: the registry's recorded paths
// included, since they are what names the branch the worktree came from.
func switchOpts(t *testing.T, f *fixture, branch string) Options {
	t.Helper()
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	o := f.opts(branch)
	o.Stack.Paths = cfg.Projects["myapp"].WorktreePaths
	return o
}

func recorded(t *testing.T, f *fixture) config.Project {
	t.Helper()
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Projects["myapp"]
}

func TestSwitchKeepsTheIndexAndDropsTheOldStack(t *testing.T) {
	f, _, _ := adoptedFixture(t)
	before := recorded(t, f).WorktreeIndices["worktree-curry"]
	path := f.foreign["worktree-curry"]

	if err := Switch(context.Background(), switchOpts(t, f, "feat/next")); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	p := recorded(t, f)
	if p.WorktreeIndices["feat/next"] != before || p.WorktreeIndices["worktree-curry"] != 0 {
		t.Fatalf("the index must move to the new branch unchanged, got %+v", p.WorktreeIndices)
	}
	if p.WorktreePaths["feat/next"] != path || p.WorktreePaths["worktree-curry"] != "" {
		t.Fatalf("the path must move with the index, got %+v", p.WorktreePaths)
	}
	if got := lastCall(f, "worktree-curry down --volumes"); got == "" {
		t.Fatalf("the old stack must go down with its volumes, got %q", f.fake.Lines())
	}
	if got := lastCall(f, "switch -c feat/next develop"); got == "" {
		t.Fatalf("an unknown branch is cut from the base, got %q", f.fake.Lines())
	}
	if got := lastCall(f, "up"); !strings.Contains(got, "feat-next") {
		t.Fatalf("the fresh stack must carry the new branch, got %q", got)
	}
}

func TestSwitchRefusesUncommittedChangesBeforeTouchingAnything(t *testing.T) {
	f, dirty, _ := adoptedFixture(t)
	*dirty = " M app.py\n"
	err := Switch(context.Background(), switchOpts(t, f, "feat/next"))
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if lastCall(f, " switch ") != "" || lastCall(f, "down") != "" {
		t.Fatalf("a refusal must touch neither git nor the stack, got %q", f.fake.Lines())
	}
}

// git goes first so that a checkout it refuses, a branch held by another
// worktree for one, leaves the old stack and its index as they were.
func TestSwitchLeavesTheStackWhenGitRefuses(t *testing.T) {
	f, _, refuse := adoptedFixture(t)
	*refuse = true
	if err := Switch(context.Background(), switchOpts(t, f, "feat/next")); err == nil {
		t.Fatal("expected git's refusal")
	}
	if lastCall(f, "down") != "" {
		t.Fatalf("the old stack must stay up, got %q", f.fake.Lines())
	}
	if recorded(t, f).WorktreeIndices["worktree-curry"] == 0 {
		t.Fatal("the index must stay with the old branch")
	}
}

// A worktree already moved by a bare `git switch -c`, or a switch that failed
// past the checkout: rerunning finishes the job without checking out again.
func TestSwitchFinishesAWorktreeThatAlreadyMoved(t *testing.T) {
	f, _, _ := adoptedFixture(t)
	f.foreign["feat/next"] = f.foreign["worktree-curry"]
	delete(f.foreign, "worktree-curry")
	f.cwd = "feat/next"

	if err := Switch(context.Background(), switchOpts(t, f, "feat/next")); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if lastCall(f, " switch ") != "" {
		t.Fatalf("the branch is already checked out, got %q", f.fake.Lines())
	}
	if recorded(t, f).WorktreeIndices["feat/next"] == 0 {
		t.Fatal("the index must follow the branch the worktree is on")
	}
}

func TestSwitchRefusesABranchThatHasItsOwnStack(t *testing.T) {
	f, _, _ := adoptedFixture(t)
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		c.Projects["myapp"].WorktreeIndices["feat/x"] = 9
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := Switch(context.Background(), switchOpts(t, f, "feat/x"))
	if err == nil || !strings.Contains(err.Error(), "already has a stack at index 9") {
		t.Fatalf("expected a refusal naming the index, got %v", err)
	}
	if lastCall(f, " switch ") != "" || lastCall(f, "down") != "" {
		t.Fatalf("a refusal must touch neither git nor the stack, got %q", f.fake.Lines())
	}
}

func TestSwitchRefusesAWorktreeWtmCreated(t *testing.T) {
	f := newFixture(t)
	f.cwd = "feat/x"
	err := Switch(context.Background(), f.opts("feat/next"))
	if err == nil || !strings.Contains(err.Error(), "wtm create feat/next") {
		t.Fatalf("expected a refusal pointing at create, got %v", err)
	}
}
