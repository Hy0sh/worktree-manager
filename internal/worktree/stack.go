package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/dockermem"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// A sweep is one kind of resource a stack leaves behind: how docker lists it,
// how docker drops it, and the noun the report uses. The three travel together
// because a listing dropped with another kind's verb compiles and lies.
type sweep struct {
	noun string
	list []string
	rm   []string
}

var (
	volumeSweep = sweep{noun: "volume", list: []string{"volume", "ls", "-q"}, rm: []string{"volume", "rm"}}
	// Only what compose built: a pulled image carries no project label, so the
	// postgres the main stack also runs can never be caught by this sweep.
	imageSweep = sweep{noun: "image", list: []string{"images", "-q"}, rm: []string{"rmi"}}
)

// removeVolumes drops the stack's volumes once the worktree is gone. `docker
// compose down`, which stop runs, deliberately keeps them: without this
// every removed worktree leaves its database behind forever.
func removeVolumes(ctx context.Context, o Options, wt stack.Worktree) {
	removeSwept(ctx, o, wt, volumeSweep)
}

// removeImages drops what the stack built once the worktree is gone. `docker
// compose down` keeps images as it keeps volumes, and a stack builds its own
// copy of every service image: without this each removal leaves gigabytes.
func removeImages(ctx context.Context, o Options, wt stack.Worktree) {
	removeSwept(ctx, o, wt, imageSweep)
}

// labelled answers nothing at all when docker cannot be reached: a caller
// cannot tell that from a stack that never came up.
func labelled(ctx context.Context, o Options, wt stack.Worktree, s sweep) []string {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append(slices.Clone(s.list), "--filter",
			"label=com.docker.compose.project="+o.projectName(wt)),
	})
	if err != nil {
		return nil
	}
	return strings.Fields(res.Stdout)
}

// removeSwept drops what the listing of that kind returned. A failure is a
// warning and not an error: the worktree is gone either way, and what is left
// behind costs disk space and nothing else.
func removeSwept(ctx context.Context, o Options, wt stack.Worktree, s sweep) {
	ids := labelled(ctx, o, wt, s)
	if len(ids) == 0 {
		return
	}
	project := o.projectName(wt)
	if _, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append(slices.Clone(s.rm), ids...),
	}); err != nil {
		o.logf("warning: %d %s(s) of %s could not be removed: %v", len(ids), s.noun, project, err)
		return
	}
	o.logf("%d %s(s) removed (%s)", len(ids), s.noun, project)
}

func start(ctx context.Context, o Options, dest string) error {
	// A repository without a compose file simply has no stack. The worktree is
	// still perfectly usable, so this is a note and not a failure.
	if !compose.Has(o.Project.Dir) {
		o.logf("no compose file in this project: no stack to start, the worktree is ready")
		return nil
	}
	// Advisory only: an average over the running stacks is not a fact worth
	// failing on, so a person is asked rather than refused, and a script is not
	// asked at all.
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
	services, err := o.Project.ServicesFor(o.Profile)
	if err != nil {
		return err
	}
	if err := o.Stack.Up(ctx, o.projectName(wt), dest, files, services); err != nil {
		return fmt.Errorf("starting the stack: %w", err)
	}
	if o.Profile != "" {
		o.logf("stack started (worktree %d, %s, profile %s)", wt.Index, o.Branch, o.Profile)
	} else {
		o.logf("stack started (worktree %d, %s)", wt.Index, o.Branch)
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
	// Recorded next to the index, and only where one may be allocated: this is
	// what later tells a worktree that switched branches from one that left.
	if mode == index.MayAllocate && wt.Path != "" {
		if err := config.RecordWorktreePath(o.Resolver.ConfigPath, o.Name, o.Branch, wt.Path); err != nil {
			o.logf("warning: the path of %s could not be recorded, a later branch switch there "+
				"will read as a worktree that vanished: %v", o.Branch, err)
		}
	}
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

// runningContainers lists what docker still runs under a stack's compose
// project. A worktree that really vanished leaves stopped containers at most;
// one that only switched branches leaves them running, since nobody stopped it.
func runningContainers(ctx context.Context, o Options, wt stack.Worktree) []string {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"ps", "-q", "--filter",
			"label=com.docker.compose.project=" + o.projectName(wt)},
	})
	if err != nil {
		return nil
	}
	return strings.Fields(res.Stdout)
}
