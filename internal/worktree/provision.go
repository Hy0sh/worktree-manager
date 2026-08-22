package worktree

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// skipDirs are never descended into when looking for *.env files.
var skipDirs = map[string]bool{".git": true, ".worktrees": true, "node_modules": true, ".claude": true}

// envMaxDepth mirrors `find -maxdepth 3` from bin/new-worktree.
const envMaxDepth = 3

// provisionMode says what to do with a file the worktree already has. Create
// works on a fresh checkout and takes the main repository as the reference;
// start must not clobber an edit made in the worktree since.
type provisionMode int

const (
	overwriteCopies provisionMode = iota
	keepWorktreeCopies
)

// provision lays down what the stack needs beside the checkout: the git-dir
// link, the .env and compose override copies, the link to the central backup.
// The two symlinks are always rewritten, they carry no local state.
func provision(ctx context.Context, o Options, dest string, mode provisionMode) error {
	// Keeping those artifacts out of git is a convenience, not something the
	// stack needs, so a repository that refuses the write still gets started.
	if err := excludeArtifacts(ctx, o); err != nil {
		o.logf("warning: wtm's own files could not be added to info/exclude, "+
			"do not commit them: %v", err)
	}
	if o.Project.GitContainer {
		if err := linkGitContainer(ctx, o, dest); err != nil {
			return err
		}
	}
	if err := copyEnvFiles(o.Project.Dir, dest, mode, o.logf); err != nil {
		return fmt.Errorf("copying .env files: %w", err)
	}
	if err := copyComposeOverrides(o.Project.Dir, dest, mode); err != nil {
		return fmt.Errorf("copying compose overrides: %w", err)
	}
	// A file-based engine reads nothing from the backup directory at runtime:
	// its dump is copied into the worktree instead of being mounted.
	if o.Project.Dump && !dbengine.IsFileBased(o.Project.BackupConfig().DBEngine) {
		if err := linkSnapshotDir(o, dest); err != nil {
			return fmt.Errorf("linking to the backup: %w", err)
		}
	}
	return nil
}

// linkGitContainer works around VirtioFS on macOS: in a linked worktree .git is
// a pointer file, which docker refuses to bind-mount onto /app/.git. The main
// repository needs its own link too, because the compose override copied into
// the worktree references ./.git-container on both sides.
func linkGitContainer(ctx context.Context, o Options, dest string) error {
	for _, target := range []struct{ repo, link string }{
		{dest, filepath.Join(dest, ".git-container")},
		{o.Project.Dir, filepath.Join(o.Project.Dir, ".git-container")},
	} {
		res, err := o.Runner.Run(ctx, execx.Cmd{
			Name: "git",
			Args: []string{"-C", target.repo, "rev-parse", "--absolute-git-dir"},
		})
		if err != nil {
			return fmt.Errorf("resolving the git-dir of %s: %w", target.repo, err)
		}
		gitDir := strings.TrimSpace(res.Stdout)
		if gitDir == "" {
			return fmt.Errorf("empty git-dir for %s", target.repo)
		}
		if err := forceSymlink(gitDir, target.link); err != nil {
			return err
		}
	}
	return nil
}

// linkSnapshotDir points the worktree at the central backup directory. It has
// to be a directory symlink: the project bind-mounts ./.db-snapshot, and a file
// symlink inside that mount would resolve inside the container and dangle.
func linkSnapshotDir(o Options, dest string) error {
	target := filepath.Join(o.BackupsDir, o.Name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	return forceSymlink(target, filepath.Join(dest, ".db-snapshot"))
}

// forceSymlink is `ln -sfn` with one restraint on what it replaces: a
// symlink, or an empty directory tree, which is what Docker materialises at
// the source of a missing bind-mount (a tree, not just one directory, when
// the source is a file inside one). A path holding real content is refused
// rather than destroyed: a repository tracking a file where wtm puts its
// artifacts is a conflict for the user to resolve, not something to delete.
func forceSymlink(target, link string) error {
	info, err := os.Lstat(link)
	switch {
	case err != nil:
		// Nothing there, nothing to replace.
	case info.Mode()&os.ModeSymlink != 0:
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("replacing %s: %w", link, err)
		}
	case info.IsDir() && emptyTree(link):
		if err := os.RemoveAll(link); err != nil {
			return fmt.Errorf("replacing %s: %w", link, err)
		}
	default:
		return fmt.Errorf("%s holds real content where wtm needs a symlink: move it away and retry", link)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("linking %s -> %s: %w", link, target, err)
	}
	return nil
}

// emptyTree reports whether path contains only directories all the way down.
func emptyTree(path string) bool {
	empty := true
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			empty = false
			return fs.SkipAll
		}
		return nil
	})
	return empty
}

func copyEnvFiles(root, dest string, mode provisionMode, logf func(string, ...any)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		depth := len(strings.Split(rel, string(os.PathSeparator)))
		if d.IsDir() {
			if skipDirs[d.Name()] || depth >= envMaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if depth > envMaxDepth || !strings.HasSuffix(d.Name(), ".env") {
			return nil
		}
		// The copy follows a valid symlink (a snapshot of the developer's
		// values is the point), but a dangling one, typically .env pointing
		// at an .env.local that does not exist yet, is the developer's state
		// and must not fail the whole provisioning.
		if d.Type()&fs.ModeSymlink != 0 {
			if _, err := os.Stat(path); err != nil {
				logf("warning: %s is a symlink whose target is missing, not copied", rel)
				return nil
			}
		}
		return copyFile(path, filepath.Join(dest, rel), mode)
	})
}

func copyComposeOverrides(root, dest string, mode provisionMode) error {
	for _, name := range compose.OverrideNames {
		src := filepath.Join(root, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, filepath.Join(dest, name), mode); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode provisionMode) error {
	if mode == keepWorktreeCopies {
		if _, err := os.Lstat(dst); err == nil {
			return nil
		}
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Never write through a symlink the checkout may have laid down at dst:
	// the copy must be the worktree's own file, and a branch-controlled link
	// could point anywhere, outside the worktree included.
	if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("replacing the symlink at %s: %w", dst, err)
		}
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}
