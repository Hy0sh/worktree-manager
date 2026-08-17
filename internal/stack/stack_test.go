package stack

import (
	"context"
	"io"
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

func newClient(t *testing.T, f *execx.Fake) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	return &Client{Runner: f, Dir: dir, Out: io.Discard}, dir
}

func TestWorktreesSkipsMainAndPositionsFromOne(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: porcelain}, nil
	}}
	c, _ := newClient(t, f)
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

func TestProjectPrefix(t *testing.T) {
	if got := ProjectPrefix("My App", 3); got != "my-app-wt-3-" {
		t.Fatalf("got %q", got)
	}
}

func TestFindByBranch(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: porcelain}, nil
	}}
	c, _ := newClient(t, f)
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

// The compose project name is the only handle on a worktree stack's containers
// and volumes, and it has to match what worktree-compose produced, character
// for character, or an existing stack becomes unreachable.
func TestProjectNameMatchesTheFormerTool(t *testing.T) {
	if got := ProjectName("my-app", 1, "feat/wtm-e2e"); got != "my-app-wt-1-feat-wtm-e2e" {
		t.Fatalf("ProjectName = %q", got)
	}
	if got := ProjectName("my-app", 3, "Fix/GAL_42--Weird"); got != "my-app-wt-3-fix-gal-42-weird" {
		t.Fatalf("ProjectName = %q", got)
	}
}

// Up and Down now talk to docker compose directly, with the project name that
// isolates this worktree's containers, network and volumes.
func TestUpTargetsTheWorktreeProject(t *testing.T) {
	f := &execx.Fake{}
	c, _ := newClient(t, f)
	files := []string{"/wt/compose.yaml", "/wt/.wtm-snapshot.yaml"}
	if err := c.Up(context.Background(), "myapp-wt-1-feat-x", "/wt", files); err != nil {
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
}

func TestDownKeepsVolumes(t *testing.T) {
	f := &execx.Fake{}
	c, _ := newClient(t, f)
	if err := c.Down(context.Background(), "myapp-wt-1-feat-x", "/wt"); err != nil {
		t.Fatalf("Down: %v", err)
	}
	line := f.Lines()[0]
	if !strings.HasSuffix(line, "compose -p myapp-wt-1-feat-x down") {
		t.Fatalf("down call = %q", line)
	}
	if strings.Contains(line, "-v") {
		t.Fatal("stopping must not destroy the database")
	}
}
