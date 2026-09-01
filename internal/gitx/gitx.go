// Package gitx answers the few git questions wtm asks about the current
// directory.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// RepoRoot returns the main repository root, even from inside a linked
// worktree: `--show-toplevel` would answer the worktree itself, which is never
// the path a project is registered under, while `--git-common-dir` is stable.
func RepoRoot(ctx context.Context, runner execx.Runner) (string, error) {
	res, err := runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"},
	})
	if err != nil {
		return "", fmt.Errorf("current directory is not a git repository: %w", err)
	}
	common := strings.TrimSpace(res.Stdout)
	if common == "" {
		return "", errors.New("git returned an empty common directory")
	}
	return filepath.Dir(filepath.Clean(common)), nil
}

// Current is where a command was typed, as git sees it.
type Current struct {
	Path   string // the working tree's own root, linked worktree or main repository
	Branch string // empty on a detached HEAD
	// Linked tells a worktree from the main repository: git keeps a linked
	// worktree's own git directory under the common one, so the two differ
	// there and are the same path everywhere else.
	Linked bool
}

// CurrentWorktree answers where the caller stands, which RepoRoot deliberately
// cannot: it resolves the main repository and so erases the very distinction
// `wtm adopt` needs.
func CurrentWorktree(ctx context.Context, runner execx.Runner) (Current, error) {
	res, err := runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"rev-parse", "--path-format=absolute",
			"--show-toplevel", "--git-dir", "--git-common-dir"},
	})
	if err != nil {
		return Current{}, fmt.Errorf("current directory is not a git repository: %w", err)
	}
	// A path can hold spaces, so the answer is read line by line and never
	// split on whitespace.
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 3 {
		return Current{}, fmt.Errorf("git answered %d path(s) where the toplevel, "+
			"the git directory and the common one were asked for", len(lines))
	}
	for i, l := range lines {
		lines[i] = filepath.Clean(strings.TrimSpace(l))
	}
	cur := Current{Path: lines[0], Linked: lines[1] != lines[2]}

	// A detached HEAD is no symbolic ref, so this exits non-zero: the ordinary
	// case of a worktree left on a commit, hence the silence.
	if res, err := runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"symbolic-ref", "--short", "-q", "HEAD"},
	}); err == nil {
		cur.Branch = strings.TrimSpace(res.Stdout)
	}
	return cur, nil
}
