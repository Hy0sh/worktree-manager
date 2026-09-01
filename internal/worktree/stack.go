package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/dockermem"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// stackVolumes lists the volumes docker labelled with this stack's compose
// project. Empty on a stack that never came up, which is how a first start is
// told from a restart, and on an unreachable docker.
func stackVolumes(ctx context.Context, o Options, wt stack.Worktree) []string {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"volume", "ls", "-q", "--filter", "label=com.docker.compose.project=" + o.projectName(wt)},
	})
	if err != nil {
		return nil
	}
	return strings.Fields(res.Stdout)
}

// removeVolumes drops the stack's volumes once the worktree is gone. `docker
// compose down`, which stop runs, deliberately keeps them: without this
// every removed worktree leaves its database behind forever.
func removeVolumes(ctx context.Context, o Options, wt stack.Worktree) {
	volumes := stackVolumes(ctx, o, wt)
	if len(volumes) == 0 {
		return
	}
	project := o.projectName(wt)
	if _, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append([]string{"volume", "rm"}, volumes...),
	}); err != nil {
		o.logf("warning: %d volume(s) of %s could not be removed: %v", len(volumes), project, err)
		return
	}
	o.logf("%d volume(s) removed (%s)", len(volumes), project)
}

// stackImages lists the images compose built for this stack. Compose labels
// what it builds with the project name; a pulled image carries no such label,
// so the postgres the main stack also runs can never be caught here.
func stackImages(ctx context.Context, o Options, wt stack.Worktree) []string {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"images", "-q", "--filter", "label=com.docker.compose.project=" + o.projectName(wt)},
	})
	if err != nil {
		return nil
	}
	return strings.Fields(res.Stdout)
}

// removeImages drops what the stack built once the worktree is gone. `docker
// compose down` keeps images as it keeps volumes, and a worktree stack builds
// its own copy of every service image under its own project name: without this
// every removed worktree leaves gigabytes behind that nothing ever reuses.
func removeImages(ctx context.Context, o Options, wt stack.Worktree) {
	images := stackImages(ctx, o, wt)
	if len(images) == 0 {
		return
	}
	project := o.projectName(wt)
	if _, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append([]string{"rmi"}, images...),
	}); err != nil {
		o.logf("warning: %d image(s) of %s could not be removed: %v", len(images), project, err)
		return
	}
	o.logf("%d image(s) removed (%s)", len(images), project)
}

func start(ctx context.Context, o Options, dest string) error {
	// A repository without a compose file simply has no stack. The worktree is
	// still perfectly usable, so this is a note and not a failure.
	if !hasCompose(o.Project.Dir) {
		o.logf("no compose file in this project: no stack to start, the worktree is ready")
		return nil
	}
	// Advisory only: the measurement can fail for a dozen harmless reasons and
	// the user knows their machine better than an extrapolation does. Which is
	// why a person is asked rather than refused, and a script is not asked at
	// all: an average over the running stacks is not a fact worth failing on.
	if u, err := dockermem.Read(ctx, o.Runner); err == nil {
		if msg := u.Warning(); msg != "" {
			o.logf("%s", msg)
			// The escape belongs to the question: an automation that allocated
			// a terminal without anybody behind it hangs here, and the only
			// trace its operator will find is this line.
			if o.Confirm != nil && !o.Confirm("start the stack anyway? (--ignore-memory never asks)") {
				o.logf("stack not started: free some memory, then `wtm start %s`", o.Branch)
				return errStackNotStarted
			}
		}
	}
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	if err := o.resolveIndex(ctx, &wt, index.MayAllocate); err != nil {
		return err
	}
	// A worktree can have lost these since it was created, by an earlier wtm
	// that did not write them or by a manual cleanup, and docker then fails on a
	// raw mount error instead of a diagnosis.
	if err := provision(ctx, o, dest, keepWorktreeCopies); err != nil {
		return err
	}
	if err := ensureSnapshotAssets(o, dest); err != nil {
		return err
	}
	if err := allocatePorts(ctx, o, wt, dest); err != nil {
		return err
	}
	files, err := composeFiles(o, dest)
	if err != nil {
		return err
	}
	fresh := len(stackVolumes(ctx, o, wt)) == 0
	if err := o.Stack.Up(ctx, o.projectName(wt), dest, files); err != nil {
		return fmt.Errorf("starting the stack: %w", err)
	}
	o.logf("stack started (worktree %d, %s)", wt.Index, o.Branch)
	// The dump carries what migrations create, never seed data; post_create
	// would contradict the note. A file-based engine restores nothing on start.
	if fresh && o.Project.Dump && o.Project.PostCreate == "" &&
		!dbengine.IsFileBased(o.Project.BackupConfig().DBEngine) {
		o.logf("note: the database was restored from the dump and holds no seed data yet,")
		o.logf("      seed it with `wtm exec %s -- <your seed command>`", o.Branch)
	}
	logEndpoints(o, wt)
	return nil
}

// logEndpoints lists the addresses of the stack. postCreate prints them a
// second time, since a seed's output buries them.
func logEndpoints(o Options, wt stack.Worktree) {
	for _, line := range endpoints(o, wt) {
		o.logf("  %s", line)
	}
}

func hasCompose(projectDir string) bool {
	_, err := compose.Base(projectDir)
	return err == nil
}

// projectName is the compose project of a worktree stack, which isolates its
// containers, network and volumes from the main stack and from one another.
func (o Options) projectName(wt stack.Worktree) string {
	return stack.ProjectName(filepath.Base(o.Project.Dir), wt.Index, wt.Branch)
}

func (o Options) resolveIndex(ctx context.Context, wt *stack.Worktree, mode index.Mode) error {
	if mode == index.MayAllocate {
		o.Resolver.Conflicts = portClash(o)
	}
	n, err := o.Resolver.Resolve(ctx, o.Branch, wt.Pos, mode)
	if err != nil {
		return err
	}
	wt.Index = n
	return nil
}

// composeFiles lists what docker compose must read, in order: the project's
// own files as they exist in the worktree, then the generated snapshot file.
func composeFiles(o Options, dest string) ([]string, error) {
	projectFiles, err := compose.Files(o.Project.Dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range projectFiles {
		files = append(files, filepath.Join(dest, filepath.Base(f)))
	}
	// A file-based engine has no snapshot override: the generated file would
	// reference a database service the project does not have.
	if o.Project.Dump && !dbengine.IsFileBased(o.Project.BackupConfig().DBEngine) {
		files = append(files, filepath.Join(dest, snapshotOverride))
	}
	if path := filepath.Join(dest, portsOverride); fileExists(path) {
		files = append(files, path)
	}
	return files, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
