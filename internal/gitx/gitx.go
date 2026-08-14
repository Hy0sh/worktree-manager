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

// RepoRoot returns the main repository root, even when called from inside a
// linked worktree.
//
// `--show-toplevel` cannot be used: inside a worktree it returns the worktree
// itself, which is never the path a project is registered under, so every
// command run from a worktree would fail to find its own project.
// `--git-common-dir` always points at the main repository's git directory.
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
