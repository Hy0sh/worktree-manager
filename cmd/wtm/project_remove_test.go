package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// porcelain is what `git worktree list --porcelain` prints: the main repository
// first, then one block per linked worktree.
func porcelain(root string, branches ...string) string {
	var b strings.Builder
	b.WriteString("worktree " + root + "\nHEAD abc\nbranch refs/heads/main\n\n")
	for _, br := range branches {
		b.WriteString("worktree " + filepath.Join(root, ".worktrees", br) + "\nHEAD def\nbranch refs/heads/" + br + "\n\n")
	}
	return b.String()
}

// listing answers the worktree listing for the project's own directory, which
// only exists once the fixture built it.
func removeApp(t *testing.T, listing func(root string) (execx.Result, error)) *app {
	t.Helper()
	dir := t.TempDir()
	return &app{
		cfg: &config.Config{Projects: map[string]config.Project{
			"myapp": {Dir: dir, PortOffset: 2000},
		}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		runner: &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
			if strings.Contains(c.String(), "worktree list") {
				return listing(dir)
			}
			return execx.Result{}, nil
		}},
		out: new(bytes.Buffer),
		in:  strings.NewReader(""),
	}
}

// Removing the entry first would orphan those worktrees: every worktree command
// needs it, and the port offset it frees is handed to the next project.
func TestProjectRemoveRefusesWhileWorktreesRemain(t *testing.T) {
	a := removeApp(t, func(root string) (execx.Result, error) {
		return execx.Result{Stdout: porcelain(root, "feat/x", "feat/y")}, nil
	})

	cmd := newProjectRemoveCmd(a)
	err := cmd.RunE(cmd, []string{"myapp"})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"feat/x", "feat/y", "wtm remove myapp", "2000"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q, got: %v", want, err)
		}
	}
	if _, ok := a.cfg.Projects["myapp"]; !ok {
		t.Fatal("the project must stay registered when the removal is refused")
	}
}

func TestProjectRemoveProceedsWithoutWorktrees(t *testing.T) {
	a := removeApp(t, func(root string) (execx.Result, error) {
		return execx.Result{Stdout: porcelain(root)}, nil
	})

	cmd := newProjectRemoveCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("removal: %v", err)
	}
	if _, ok := a.cfg.Projects["myapp"]; ok {
		t.Fatal("the project should be gone")
	}
}

// A directory git cannot answer for (moved, or never a repository) must not
// trap the entry: the only way out would be editing config.json by hand.
func TestProjectRemoveProceedsWhenGitCannotAnswer(t *testing.T) {
	a := removeApp(t, func(string) (execx.Result, error) {
		return execx.Result{ExitCode: 128}, errors.New("not a git repository")
	})

	cmd := newProjectRemoveCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("removal: %v", err)
	}
	if _, ok := a.cfg.Projects["myapp"]; ok {
		t.Fatal("the project should be gone")
	}
}
