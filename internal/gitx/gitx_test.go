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
