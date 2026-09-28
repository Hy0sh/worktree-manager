package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Typed from inside a worktree, the branch is the one checked out there:
// making someone spell it out again only invites naming the wrong one.
func TestPathTakesTheBranchOfTheWorktreeItIsTypedFrom(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, ".worktrees", "feat", "x")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		switch {
		case strings.Contains(line, "--show-toplevel"):
			return execx.Result{Stdout: strings.Join([]string{
				wt, filepath.Join(dir, ".git", "worktrees", "x"), filepath.Join(dir, ".git"),
			}, "\n") + "\n"}, nil
		case strings.Contains(line, "--git-common-dir"):
			return execx.Result{Stdout: filepath.Join(dir, ".git") + "\n"}, nil
		case strings.Contains(line, "symbolic-ref"):
			return execx.Result{Stdout: "feat/x\n"}, nil
		case strings.Contains(line, "worktree list"):
			return execx.Result{Stdout: porcelainOf(dir, "worktree "+wt+"\nHEAD abc\nbranch refs/heads/feat/x")}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newPathCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("path: %v", err)
	}
	if out.String() != wt+"\n" {
		t.Fatalf("output = %q, want %q", out.String(), wt+"\n")
	}
}

// From the main checkout there is no branch to take, and the main one is not
// a worktree wtm could answer about.
func TestPathWithoutABranchRefusesTheMainCheckout(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, _ := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "--show-toplevel"):
			return execx.Result{Stdout: strings.Join([]string{
				dir, filepath.Join(dir, ".git"), filepath.Join(dir, ".git"),
			}, "\n") + "\n"}, nil
		case strings.Contains(line, "--git-common-dir"):
			return execx.Result{Stdout: filepath.Join(dir, ".git") + "\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newPathCmd(a)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "name the branch") {
		t.Fatalf("err = %v, want to be told to name the branch", err)
	}
}

// `wtm path` exists to be substituted: `cd $(wtm path feat/x)`. Whatever
// diagnosis a detached worktree deserves elsewhere, a second line here lands in
// the shell's argument.
func TestPathPrintsNothingButThePath(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: "worktree " + dir + "\nHEAD abc\nbranch refs/heads/main\n\n" +
				"worktree " + filepath.Join(dir, ".worktrees", "refactor/x") +
				"\nHEAD 37a276b48e772823\ndetached\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newPathCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp", "refactor/x"}); err != nil {
		t.Fatalf("path: %v", err)
	}

	want := filepath.Join(dir, ".worktrees", "refactor/x") + "\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}
