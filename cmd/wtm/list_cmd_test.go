package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// A blank BRANCH column reads as a bug in wtm rather than as what it is: a
// worktree whose HEAD was moved off its branch, by a `git checkout <rev>` there.
func TestListNamesTheBranchAndTheDetachedHead(t *testing.T) {
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

	cmd := newListCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	if !strings.Contains(out.String(), "refactor/x (detached 37a276b4)") {
		t.Fatalf("the branch of the path and where HEAD sits must both show:\n%s", out.String())
	}
}

// porcelainOf builds the answer of `git worktree list --porcelain` for a
// repository and the linked worktrees given as path -> ref lines.
func porcelainOf(dir string, linked ...string) string {
	out := "worktree " + dir + "\nHEAD abc\nbranch refs/heads/main\n"
	for _, block := range linked {
		out += "\n" + block + "\n"
	}
	return out
}

func TestListPointsAtTheWorktreesLeftToAdopt(t *testing.T) {
	dir := t.TempDir()
	curry := filepath.Join(dir, ".claude", "worktrees", "curry")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: porcelainOf(dir,
				"worktree "+curry+"\nHEAD 4e841725\nbranch refs/heads/worktree-curry")}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newListCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	if !strings.Contains(out.String(), "worktree-curry") || !strings.Contains(out.String(), "adoptable") {
		t.Fatalf("a worktree left to adopt belongs in the listing:\n%s", out.String())
	}
	// The word alone names no verb, and the branch is what adopt takes.
	if !strings.Contains(out.String(), "wtm adopt <branch>") {
		t.Fatalf("the listing must name the verb that brings one in:\n%s", out.String())
	}
}

// A worktree wtm created carries its stack, so the reminder would send the
// reader towards a verb that refuses it.
func TestListMentionsAdoptOnlyWhenSomethingIsLeftToAdopt(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: porcelainOf(dir,
				"worktree "+filepath.Join(dir, ".worktrees", "feat", "x")+
					"\nHEAD abc\nbranch refs/heads/feat/x")}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newListCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	if strings.Contains(out.String(), "wtm adopt") {
		t.Fatalf("nothing is left to adopt here:\n%s", out.String())
	}
}

// git names no branch for a detached worktree, and derives none from the path
// outside wtm's own root. The BRANCH column then held the parenthesis alone.
func TestListNamesADetachedWorktreeLeftToAdopt(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: porcelainOf(dir,
				"worktree "+filepath.Join(dir, "elsewhere")+"\nHEAD 37a276b48e772823\ndetached")}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newListCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	row := regexp.MustCompile(`(?m)^-\s+\(detached 37a276b4\)\s+adoptable`)
	if !row.MatchString(out.String()) {
		t.Fatalf("where HEAD sits is all there is to name, and it opens the BRANCH column:\n%s", out.String())
	}
}
