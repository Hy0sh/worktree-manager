package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/mark"
)

var artifactBlock = mark.Block{
	Start: "# --- wtm artifacts ---",
	End:   "# --- end wtm ---",
}

// excluded lists what wtm drops in a checkout: its artifacts in a worktree,
// and the .worktrees directory in the main one, which `git add -A` there adds
// as an embedded repository. None of it is in any project's .gitignore, so
// without this a commit takes wtm's plumbing along with the work.
var excluded = []string{".worktrees/", ".git-container", ".db-snapshot", snapshotOverride, portsOverride}

// excludeArtifacts writes to info/exclude rather than .gitignore, which is
// versioned and belongs to the project. git reads it from the common git-dir,
// shared by the main checkout and every worktree, so one write covers them all.
func excludeArtifacts(ctx context.Context, o Options) error {
	dir, err := commonGitDir(ctx, o)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "info", "exclude")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	// Nothing to change: no write at all, so two worktrees created at the same
	// moment cannot overwrite each other's read of a file they share.
	next := artifactBlock.Rewrite(string(existing), excluded)
	if next == string(existing) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(next), 0o644)
}

// commonGitDir is the git directory the main checkout shares with its
// worktrees. --git-common-dir answers relative to the working directory on some
// versions, hence the join.
func commonGitDir(ctx context.Context, o Options) (string, error) {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", o.Project.Dir, "rev-parse", "--git-common-dir"},
	})
	if err != nil {
		return "", fmt.Errorf("resolving the common git-dir of %s: %w", o.Project.Dir, err)
	}
	dir := strings.TrimSpace(res.Stdout)
	if dir == "" {
		return "", fmt.Errorf("empty common git-dir for %s", o.Project.Dir)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(o.Project.Dir, dir)
	}
	return dir, nil
}
