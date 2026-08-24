package stack

import (
	"context"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

const porcelain = `worktree /repo/myapp
HEAD abc123
branch refs/heads/develop

worktree /repo/myapp/.worktrees/feat-a
HEAD def456
branch refs/heads/feat/a

worktree /repo/myapp/.worktrees/feat-b
HEAD 789abc
branch refs/heads/feat/b
`

func TestWorktreesSkipsMainAndPositionsFromOne(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: porcelain}, nil
	}}
	c := &Client{Runner: f, Dir: "/repo/myapp"}
	wts, err := c.Worktrees(context.Background())
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("expected 2 non-main worktrees, got %d", len(wts))
	}
	if wts[0].Pos != 1 || wts[0].Branch != "feat/a" {
		t.Fatalf("first worktree = %+v", wts[0])
	}
	if wts[1].Pos != 2 || wts[1].Path != "/repo/myapp/.worktrees/feat-b" {
		t.Fatalf("second worktree = %+v", wts[1])
	}
	if got := f.Lines()[0]; !strings.Contains(got, "worktree list --porcelain") {
		t.Fatalf("expected a porcelain listing, got %q", got)
	}
}

func TestWorktreesLeaveIndexToTheResolver(t *testing.T) {
	fake := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "worktree /repo\nbranch refs/heads/main\n\n" +
			"worktree /repo/.worktrees/b\nbranch refs/heads/b\n\n" +
			"worktree /repo/.worktrees/a\nbranch refs/heads/a\n"}, nil
	}}
	c := &Client{Runner: fake, Dir: "/repo"}
	wts, err := c.Worktrees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("expected 2 linked worktrees, got %d", len(wts))
	}
	for i, wt := range wts {
		if wt.Index != 0 {
			t.Fatalf("worktree %d: Index must stay zero until the resolver fills it, got %d", i, wt.Index)
		}
		if wt.Pos != i+1 {
			t.Fatalf("worktree %d: Pos must be the 1-based listing position, got %d", i, wt.Pos)
		}
	}
}

func TestFindByBranch(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: porcelain}, nil
	}}
	c := &Client{Runner: f, Dir: "/repo/myapp"}
	wt, err := c.FindByBranch(context.Background(), "feat/b")
	if err != nil {
		t.Fatalf("FindByBranch: %v", err)
	}
	if wt.Pos != 2 {
		t.Fatalf("pos = %d, want 2", wt.Pos)
	}
	if _, err := c.FindByBranch(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for an unknown branch")
	} else if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should mention the branch, got %q", err.Error())
	}
}

// `worktree list --porcelain` reports a lock as `locked` alone or `locked
// <reason>`, and only `remove -f -f` gets past one.
func TestWorktreesCarryTheLock(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "worktree /repo\nbranch refs/heads/main\n\n" +
			"worktree /repo/.worktrees/a\nbranch refs/heads/a\n" +
			"locked claude session a (pid 79510)\n\n" +
			"worktree /repo/.worktrees/b\nbranch refs/heads/b\nlocked\n\n" +
			"worktree /repo/.worktrees/c\nbranch refs/heads/c\n"}, nil
	}}
	c := &Client{Runner: f, Dir: "/repo"}
	wts, err := c.Worktrees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 3 {
		t.Fatalf("expected 3 linked worktrees, got %d", len(wts))
	}
	if !wts[0].Locked || wts[0].LockReason != "claude session a (pid 79510)" {
		t.Fatalf("first worktree = %+v", wts[0])
	}
	if !wts[1].Locked || wts[1].LockReason != "" {
		t.Fatalf("a reasonless lock is still a lock, got %+v", wts[1])
	}
	if wts[2].Locked || wts[2].LockReason != "" {
		t.Fatalf("the lock must not leak into the next worktree, got %+v", wts[2])
	}
}

// `claude -w` puts its worktrees under .claude/worktrees. wtm has no index, no
// provisioned .env and no stack for those, so listing them only offers commands
// that cannot work.
func TestWorktreesSkipsWhatWtmDidNotCreate(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "worktree /repo\nbranch refs/heads/main\n\n" +
			"worktree /repo/.claude/worktrees/curry\nbranch refs/heads/worktree-curry\n\n" +
			"worktree /elsewhere/manual\nbranch refs/heads/manual\n\n" +
			"worktree /repo/.worktrees/feat/x\nbranch refs/heads/feat/x\n"}, nil
	}}
	c := &Client{Runner: f, Dir: "/repo"}
	wts, err := c.Worktrees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 1 {
		t.Fatalf("expected only the worktree under .worktrees, got %+v", wts)
	}
	if wts[0].Branch != "feat/x" || wts[0].Pos != 1 {
		t.Fatalf("positions must stay contiguous over the kept worktrees, got %+v", wts[0])
	}
}
