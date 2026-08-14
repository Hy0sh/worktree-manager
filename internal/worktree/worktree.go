// Package worktree creates, starts, stops and removes project worktrees. It is
// the Go port of bin/new-worktree, with the docker side delegated to wtc.
package worktree

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dockermem"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/wtc"
)

// composeOverrides are copied verbatim when present, like bin/new-worktree did.
var composeOverrides = []string{"compose.override.yaml", "docker-compose.override.yml"}

// skipDirs are never descended into when looking for *.env files.
var skipDirs = map[string]bool{".git": true, ".worktrees": true, "node_modules": true, ".claude": true}

// envMaxDepth mirrors `find -maxdepth 3` from bin/new-worktree.
const envMaxDepth = 3

// Options carries everything a worktree command needs.
type Options struct {
	Name       string // project name in the registry
	Project    config.Project
	Branch     string
	Base       string
	NoStart    bool
	Force      bool // remove despite uncommitted tracked changes
	BackupsDir string
	Runner     execx.Runner
	Out        io.Writer
	Wtc        *wtc.Client
}

func (o Options) dest() string {
	return filepath.Join(o.Project.Dir, ".worktrees", o.Branch)
}

func (o Options) logf(format string, args ...any) {
	if o.Out != nil {
		fmt.Fprintf(o.Out, format+"\n", args...)
	}
}

// Create builds the worktree and, unless NoStart, brings its stack up.
func Create(ctx context.Context, o Options) error {
	dest := o.dest()
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("worktree %s already exists, remove it first (`wtm remove %s`)", dest, o.Branch)
	}

	if err := addWorktree(ctx, o, dest); err != nil {
		return err
	}
	if o.Project.GitContainer {
		if err := linkGitContainer(ctx, o, dest); err != nil {
			return err
		}
	}
	if err := copyEnvFiles(o.Project.Dir, dest); err != nil {
		return fmt.Errorf("copying .env files: %w", err)
	}
	if err := copyComposeOverrides(o.Project.Dir, dest); err != nil {
		return fmt.Errorf("copying compose overrides: %w", err)
	}
	if o.Project.Dump {
		if err := linkSnapshotDir(o, dest); err != nil {
			return fmt.Errorf("linking to the backup: %w", err)
		}
	}
	if err := ensureSnapshotAssets(o, dest); err != nil {
		return err
	}
	o.logf("worktree ready: %s", dest)

	if o.NoStart {
		o.logf("stack not started (--no-start), run `wtm create %s` without the flag to start it", o.Branch)
		return nil
	}
	return start(ctx, o, dest)
}

// Start brings an existing worktree's stack back up. Without it, restarting a
// stopped worktree means calling wtc with the index it derives, which is
// exactly the internal knowledge this tool exists to hide.
func Start(ctx context.Context, o Options) error {
	wt, err := o.Wtc.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	return start(ctx, o, wt.Path)
}

// ensureSnapshotAssets writes the restore script and the generated compose
// file. Idempotent, and run on every start so a worktree created before they
// existed picks them up too.
func ensureSnapshotAssets(o Options, dest string) error {
	if !o.Project.Dump {
		return nil
	}
	if err := writeRestoreScript(o.BackupsDir, o.Name); err != nil {
		return fmt.Errorf("writing the restore script: %w", err)
	}
	if err := writeSnapshotOverride(dest, o.Project.BackupConfig().DBService); err != nil {
		return fmt.Errorf("writing the snapshot compose file: %w", err)
	}
	return nil
}

// Stop takes the stack down and leaves the worktree in place.
func Stop(ctx context.Context, o Options) error {
	wt, err := o.Wtc.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	if err := o.Wtc.Stop(ctx, wt.Index); err != nil {
		return fmt.Errorf("stopping the stack: %w", err)
	}
	o.logf("stack stopped (worktree %d, %s)", wt.Index, o.Branch)
	return nil
}

// Remove stops the stack then removes the worktree, keeping the local branch.
func Remove(ctx context.Context, o Options) error {
	wt, err := o.Wtc.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	// A worktree created with --no-start on a project without the
	// devDependency must still be removable.
	if err := o.Wtc.EnsureAvailable(); err != nil {
		o.logf("warning: %v (removing without stopping the stack)", err)
	} else if err := o.Wtc.Stop(ctx, wt.Index); err != nil {
		return fmt.Errorf("stopping the stack: %w", err)
	}

	// The worktree always holds untracked files this tool created (.env copies,
	// .git-container, .db-snapshot), and git refuses to remove a worktree that
	// has any. Only tracked changes represent work worth protecting, so check
	// those and force past the rest.
	if !o.Force {
		changes, err := trackedChanges(ctx, o, wt.Path)
		if err != nil {
			return err
		}
		if changes != "" {
			return fmt.Errorf("worktree %s has uncommitted changes:\n%s\ncommit them, or rerun with --force", wt.Path, changes)
		}
	}
	if _, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", o.Project.Dir, "worktree", "remove", "--force", wt.Path},
		Live: true,
	}); err != nil {
		return fmt.Errorf("removing the worktree (the stack is already stopped): %w", err)
	}
	pruneEmptyParents(wt.Path, filepath.Join(o.Project.Dir, ".worktrees"))
	o.logf("worktree removed: %s (branch %s kept)", wt.Path, o.Branch)
	removeVolumes(ctx, o, wt)
	return nil
}

// removeVolumes drops the stack's volumes once the worktree is gone. `docker
// compose down`, which wtc runs on stop, deliberately keeps them: without this
// every removed worktree leaves its database behind forever.
func removeVolumes(ctx context.Context, o Options, wt wtc.Worktree) {
	project := wtc.ProjectName(filepath.Base(o.Project.Dir), wt.Index, wt.Branch)
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"volume", "ls", "-q", "--filter", "label=com.docker.compose.project=" + project},
	})
	if err != nil {
		return
	}
	volumes := strings.Fields(res.Stdout)
	if len(volumes) == 0 {
		return
	}
	if _, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append([]string{"volume", "rm"}, volumes...),
	}); err != nil {
		o.logf("warning: %d volume(s) of %s could not be removed: %v", len(volumes), project, err)
		return
	}
	o.logf("%d volume(s) removed (%s)", len(volumes), project)
}

// pruneEmptyParents drops the directories a slashed branch name created
// (.worktrees/feat for feat/x), which git leaves behind empty. It stops at the
// first non-empty directory and never touches root itself.
func pruneEmptyParents(path, root string) {
	for dir := filepath.Dir(path); strings.HasPrefix(dir, root+string(os.PathSeparator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

// trackedChanges reports uncommitted changes to tracked files, ignoring the
// untracked artifacts wtm itself put there.
func trackedChanges(ctx context.Context, o Options, wtPath string) (string, error) {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", wtPath, "status", "--porcelain", "--untracked-files=no"},
	})
	if err != nil {
		return "", fmt.Errorf("worktree status: %w", err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

func addWorktree(ctx context.Context, o Options, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
	}
	args := []string{"-C", o.Project.Dir, "worktree", "add"}
	if branchExists(ctx, o) {
		o.logf("branch %s already exists locally: reused as-is, base %q ignored", o.Branch, o.Base)
		args = append(args, dest, o.Branch)
	} else {
		args = append(args, "-b", o.Branch, dest, o.Base)
	}
	if _, err := o.Runner.Run(ctx, execx.Cmd{Name: "git", Args: args, Live: true}); err != nil {
		return fmt.Errorf("creating the worktree: %w", err)
	}
	return nil
}

func branchExists(ctx context.Context, o Options) bool {
	_, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", o.Project.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/" + o.Branch},
	})
	return err == nil
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

func start(ctx context.Context, o Options, dest string) error {
	if err := o.Wtc.EnsureAvailable(); err != nil {
		return err
	}
	// Advisory only: the measurement can fail for a dozen harmless reasons and
	// the user knows their machine better than an extrapolation does.
	if u, err := dockermem.Read(ctx, o.Runner); err == nil {
		if msg := u.Warning(); msg != "" {
			o.logf("%s", msg)
		}
	}
	wt, err := o.Wtc.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	// Hand docker the generated snapshot file on top of the project's own.
	if err := ensureSnapshotAssets(o, dest); err != nil {
		return err
	}
	if o.Project.Dump {
		env, err := composeFileEnv(o.Project.Dir, dest)
		if err != nil {
			return err
		}
		o.Wtc.Env = append(o.Wtc.Env, env)
	}
	if err := o.Wtc.Start(ctx, wt.Index); err != nil {
		return fmt.Errorf("starting the stack: %w", err)
	}
	o.logf("stack started (worktree %d, %s)", wt.Index, o.Branch)
	for _, line := range endpoints(o.Project.Dir, dest) {
		o.logf("  %s", line)
	}
	return nil
}

// endpoints pairs each service of the compose file with the port it actually
// listens on in this worktree, so the output is a list of addresses to open
// rather than the raw block of variables wtc wrote into .env.
func endpoints(projectDir, worktreeDir string) []string {
	base, err := compose.Base(projectDir)
	if err != nil {
		return nil
	}
	services, err := compose.ServicePorts(base)
	if err != nil {
		return nil
	}
	allocated, err := wtc.ReadPortValues(worktreeDir)
	if err != nil {
		return nil
	}

	// A service can publish several ports (mailhog exposes SMTP and a web UI),
	// and repeating its bare name would leave no way to tell them apart. The
	// variable name carries the distinction, so use it as a suffix.
	count := map[string]int{}
	for _, s := range services {
		count[s.Service]++
	}

	type entry struct{ label, address string }
	var entries []entry
	width := 0
	for _, s := range services {
		port := allocated[s.Var]
		if s.Var == "" || port == "" {
			continue // hardcoded port: wtc could not isolate it, doctor says so
		}
		label := s.Service
		if count[s.Service] > 1 {
			label += "/" + compose.PortLabel(s)
		}
		address := "localhost:" + port
		if s.IsWeb() {
			address = "http://" + address
		}
		if len(label) > width {
			width = len(label)
		}
		entries = append(entries, entry{label, address})
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%-*s  %s", width, e.label, e.address))
	}
	return out
}

// forceSymlink is `ln -sfn`: replace whatever is there.
func forceSymlink(target, link string) error {
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replacing %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("linking %s -> %s: %w", link, target, err)
	}
	return nil
}

func copyEnvFiles(root, dest string) error {
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
		return copyFile(path, filepath.Join(dest, rel))
	})
}

func copyComposeOverrides(root, dest string) error {
	for _, name := range composeOverrides {
		src := filepath.Join(root, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
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
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// Exec runs a command inside the worktree's application container. Doing it by
// hand means knowing the compose project name wtc derives, which is internal
// knowledge no user should need.
func Exec(ctx context.Context, o Options, service string, command []string) error {
	wt, err := o.Wtc.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	if service == "" {
		service = o.Project.BackupConfig().AppService
	}
	if service == "" {
		return fmt.Errorf("no application service known for this project: pass --service, " +
			"or set app_service in its config")
	}
	project := wtc.ProjectName(filepath.Base(o.Project.Dir), wt.Index, wt.Branch)
	args := append([]string{"compose", "-p", project, "exec", service}, command...)
	_, err = o.Runner.Run(ctx, execx.Cmd{
		Name:        "docker",
		Args:        args,
		Dir:         wt.Path,
		Interactive: true,
	})
	return err
}
