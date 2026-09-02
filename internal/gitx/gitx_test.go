package gitx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestRepoRootFromTheMainRepository(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "/home/dev/myapp/.git\n"}, nil
	}}
	got, err := RepoRoot(context.Background(), f)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	if got != "/home/dev/myapp" {
		t.Fatalf("RepoRoot = %q", got)
	}
	if line := f.Lines()[0]; !strings.Contains(line, "--git-common-dir") {
		t.Fatalf("must ask git for the common dir, got %q", line)
	}
	if strings.Contains(f.Lines()[0], "--show-toplevel") {
		t.Fatal("--show-toplevel returns the worktree, not the registered project")
	}
}

// The whole point: from inside a linked worktree, git reports the main
// repository's git directory, so the project is still found.
func TestRepoRootFromInsideAWorktree(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "/home/dev/myapp/.git\n"}, nil
	}}
	got, err := RepoRoot(context.Background(), f)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	if got != "/home/dev/myapp" {
		t.Fatalf("RepoRoot = %q, a worktree must resolve to the main repository", got)
	}
}

func TestRepoRootOutsideAnyRepository(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
		return execx.Result{}, errors.New("not a git repository")
	}}
	if _, err := RepoRoot(context.Background(), f); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRepoRootRejectsEmptyOutput(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "  \n"}, nil
	}}
	if _, err := RepoRoot(context.Background(), f); err == nil {
		t.Fatal("empty output should not resolve to a path")
	}
}

// currentFake answers the two commands CurrentWorktree issues: the rev-parse
// carrying three paths, and the symbolic-ref carrying the branch. An empty
// branch stands for a detached HEAD, where symbolic-ref exits non-zero.
func currentFake(top, gitDir, common, branch string) *execx.Fake {
	return &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "symbolic-ref") {
			if branch == "" {
				return execx.Result{ExitCode: 1}, errors.New("HEAD is not a symbolic ref")
			}
			return execx.Result{Stdout: branch + "\n"}, nil
		}
		return execx.Result{Stdout: strings.Join([]string{top, gitDir, common}, "\n") + "\n"}, nil
	}}
}

func TestCurrentWorktreeInTheMainRepository(t *testing.T) {
	f := currentFake("/home/dev/myapp", "/home/dev/myapp/.git", "/home/dev/myapp/.git", "develop")
	got, err := CurrentWorktree(context.Background(), f)
	if err != nil {
		t.Fatalf("CurrentWorktree: %v", err)
	}
	if got.Linked {
		t.Fatal("the main repository is not a linked worktree")
	}
	if got.Path != "/home/dev/myapp" || got.Branch != "develop" {
		t.Fatalf("CurrentWorktree = %+v", got)
	}
}

func TestCurrentWorktreeInALinkedWorktree(t *testing.T) {
	f := currentFake("/home/dev/myapp/.worktrees/feat/x",
		"/home/dev/myapp/.git/worktrees/x", "/home/dev/myapp/.git", "feat/x")
	got, err := CurrentWorktree(context.Background(), f)
	if err != nil {
		t.Fatalf("CurrentWorktree: %v", err)
	}
	if !got.Linked {
		t.Fatal("a git dir under the common one is a linked worktree")
	}
	if got.Path != "/home/dev/myapp/.worktrees/feat/x" {
		t.Fatalf("Path = %q, must be the worktree itself and not the repository", got.Path)
	}
}

// A worktree `claude -w` left on a detached HEAD has no branch, and that is not
// an error: the caller decides what to do about it.
func TestCurrentWorktreeOnADetachedHead(t *testing.T) {
	f := currentFake("/home/dev/myapp/x", "/home/dev/myapp/.git/worktrees/x", "/home/dev/myapp/.git", "")
	got, err := CurrentWorktree(context.Background(), f)
	if err != nil {
		t.Fatalf("CurrentWorktree: %v", err)
	}
	if got.Branch != "" {
		t.Fatalf("Branch = %q, a detached HEAD names none", got.Branch)
	}
	if !got.Linked {
		t.Fatal("a detached HEAD says nothing about being linked")
	}
}

func TestCurrentWorktreeOutsideAnyRepository(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
		return execx.Result{}, errors.New("not a git repository")
	}}
	if _, err := CurrentWorktree(context.Background(), f); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCurrentWorktreeRejectsAShortAnswer(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "/home/dev/myapp\n"}, nil
	}}
	if _, err := CurrentWorktree(context.Background(), f); err == nil {
		t.Fatal("two missing paths should not resolve")
	}
}

// Paths hold spaces, so the three lines cannot be split on whitespace.
func TestCurrentWorktreeKeepsSpacesInPaths(t *testing.T) {
	f := currentFake("/home/dev/my app", "/home/dev/my app/.git", "/home/dev/my app/.git", "develop")
	got, err := CurrentWorktree(context.Background(), f)
	if err != nil {
		t.Fatalf("CurrentWorktree: %v", err)
	}
	if got.Path != "/home/dev/my app" {
		t.Fatalf("Path = %q", got.Path)
	}
}
