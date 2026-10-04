package worktree

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/backup"
	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/safefile"
)

// gitContainerLink and snapshotLink are the two symlinks wtm lays beside a
// checkout. Named here because three places need them to agree: what is laid
// down, what git is told to ignore, and what a removal takes back out.
const (
	gitContainerLink = ".git-container"
	snapshotLink     = ".db-snapshot"
)

// skipDirs are never descended into when looking for *.env files.
var skipDirs = map[string]bool{".git": true, ".worktrees": true, "node_modules": true, ".claude": true}

// envMaxDepth bounds the walk looking for .env files: three levels covers a
// repository root, a service directory and one below it, and stops wtm from
// reading a whole monorepo to find them.
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
	if err := copyListed(ctx, o, dest, mode); err != nil {
		return fmt.Errorf("copying the files listed in copy: %w", err)
	}
	if err := copyComposeOverrides(o.Project.Dir, dest, mode, o.logf); err != nil {
		return fmt.Errorf("copying compose overrides: %w", err)
	}
	if o.mountsSnapshot() {
		if err := linkSnapshotDir(o, dest); err != nil {
			return fmt.Errorf("linking to the backup: %w", err)
		}
	}
	return nil
}

// linkGitContainer works around VirtioFS on macOS: in a linked worktree .git is
// a pointer file docker refuses to bind-mount onto /app/.git. The main repository
// needs one too: the copied compose override names ./.git-container on both sides.
func linkGitContainer(ctx context.Context, o Options, dest string) error {
	for _, target := range []struct{ repo, link string }{
		{dest, filepath.Join(dest, gitContainerLink)},
		{o.Project.Dir, filepath.Join(o.Project.Dir, gitContainerLink)},
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
	target, err := backup.ProjectDir(o.BackupsDir, o.Name)
	if err != nil {
		return err
	}
	return forceSymlink(target, filepath.Join(dest, snapshotLink))
}

// forceSymlink is `ln -sfn` restrained to what it may replace: a symlink, or the
// empty directory tree Docker materialises at the source of a missing bind-mount.
// Real content is a conflict for the user to resolve, not something to delete.
func forceSymlink(target, link string) error {
	info, err := os.Lstat(link)
	switch {
	case err != nil:
		// Nothing there, nothing to replace.
	case info.Mode()&os.ModeSymlink != 0:
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("replacing %s: %w", link, err)
		}
	case info.IsDir():
		if offender := emptyTree(link); offender != "" {
			return fmt.Errorf("%s holds real content (%s) where wtm needs a symlink: move it away and retry", link, offender)
		}
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

// emptyTree returns the first real file under path, "" when there is none.
// Finder's .DS_Store does not count: it is metadata the user never chose to
// put there, and RemoveAll deletes it along with the tree.
func emptyTree(path string) (offender string) {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			offender = p
			return fs.SkipAll
		}
		if d.IsDir() || d.Name() == ".DS_Store" {
			return nil
		}
		offender = p
		return fs.SkipAll
	})
	return offender
}

func copyEnvFiles(root, dest string, mode provisionMode, logf func(string, ...any)) error {
	// Resolved once: every linked .env is checked against it below.
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
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
		// Depth needs no check here: the directories above were pruned already.
		if !strings.HasSuffix(d.Name(), ".env") {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 && !linkStaysInside(rootReal, path, rel, logf) {
			return nil
		}
		return copyFile(path, dest, filepath.Join(dest, rel), mode)
	})
}

// linkStaysInside follows a symlink (.env -> .env.local) only inside the
// project: a cloned branch controls these links, and a target outside the
// repository is content the user never put there.
func linkStaysInside(rootReal, path, rel string, logf func(string, ...any)) bool {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		logf("warning: %s is a symlink whose target is missing, not copied", rel)
		return false
	}
	if !safefile.Within(rootReal, target) {
		logf("warning: %s links outside the project (%s), not copied", rel, target)
		return false
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		logf("warning: %s does not resolve to a regular file, not copied", rel)
		return false
	}
	return true
}

// copyListed copies the files the project names in `copy`. A tracked match is
// the branch's own and stays: `*.json` would otherwise swap package.json for
// the main checkout's.
func copyListed(ctx context.Context, o Options, dest string, mode provisionMode) error {
	if len(o.Project.Copy) == 0 {
		return nil
	}
	root := o.Project.Dir
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for _, pattern := range o.Project.Copy {
		// Checked here too: config.json can be hand-edited, and the pattern
		// is joined under the project root.
		if err := config.ValidateCopyPattern(pattern); err != nil {
			o.logf("warning: %v, skipped", err)
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, path := range matches {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			// Glob walks through a directory symlink in the middle of the pattern,
			// which the check on the leaf never sees.
			dir, err := filepath.EvalSymlinks(filepath.Dir(path))
			switch {
			case err != nil || !safefile.Within(rootReal, dir):
				o.logf("warning: %s lies behind a symlink leading out of the project, not copied", rel)
				continue
			case info.IsDir():
				o.logf("warning: %s is a directory, not copied: name the files in it", rel)
				continue
			case info.Mode()&fs.ModeSymlink != 0 && !linkStaysInside(rootReal, path, rel, o.logf):
				continue
			case tracked(ctx, o, dest, rel):
				o.logf("warning: %s is tracked by git, the branch's own copy is kept", rel)
				continue
			}
			if err := copyFile(path, dest, filepath.Join(dest, rel), mode); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyComposeOverrides(root, dest string, mode provisionMode, logf func(string, ...any)) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for _, name := range compose.OverrideNames {
		src := filepath.Join(root, name)
		info, err := os.Lstat(src)
		if err != nil {
			continue
		}
		// Same rule as the .env files: a link is followed only inside the project.
		if info.Mode()&fs.ModeSymlink != 0 && !linkStaysInside(rootReal, src, name, logf) {
			continue
		}
		// The one wtm wrote before the project had its own is no local edit.
		m := mode
		if generatedByWtm(filepath.Join(dest, name)) {
			m = overwriteCopies
		}
		if err := copyFile(src, dest, filepath.Join(dest, name), m); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, root, dst string, mode provisionMode) error {
	if mode == keepWorktreeCopies {
		// Only a regular file (or a directory conflict, left to fail loudly
		// later) is worth keeping: a symlink is never a local edit, it is the
		// state that lets a later write escape the worktree.
		if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink == 0 {
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
	return safefile.Write(root, dst, data, info.Mode().Perm())
}
