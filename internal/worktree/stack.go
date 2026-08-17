package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/dockermem"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// hasVolumes reports whether this stack already has volumes, which tells a
// restart from a first start.
func hasVolumes(ctx context.Context, o Options, wt stack.Worktree) bool {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"volume", "ls", "-q", "--filter", "label=com.docker.compose.project=" + o.projectName(wt)},
	})
	return err == nil && strings.TrimSpace(res.Stdout) != ""
}

// removeVolumes drops the stack's volumes once the worktree is gone. `docker
// compose down`, which wtc runs on stop, deliberately keeps them: without this
// every removed worktree leaves its database behind forever.
func removeVolumes(ctx context.Context, o Options, wt stack.Worktree) {
	project := stack.ProjectName(filepath.Base(o.Project.Dir), wt.Index, wt.Branch)
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

func start(ctx context.Context, o Options, dest string) error {
	// A repository without a compose file simply has no stack. The worktree is
	// still perfectly usable, so this is a note and not a failure.
	if !hasCompose(o.Project.Dir) {
		o.logf("no compose file in this project: no stack to start, the worktree is ready")
		return nil
	}
	// Advisory only: the measurement can fail for a dozen harmless reasons and
	// the user knows their machine better than an extrapolation does.
	if u, err := dockermem.Read(ctx, o.Runner); err == nil {
		if msg := u.Warning(); msg != "" {
			o.logf("%s", msg)
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
	fresh := !hasVolumes(ctx, o, wt)
	if err := o.Stack.Up(ctx, o.projectName(wt), dest, files); err != nil {
		return fmt.Errorf("starting the stack: %w", err)
	}
	o.logf("stack started (worktree %d, %s)", wt.Index, o.Branch)
	// The dump carries what the migrations create, never seed data, so a
	// brand new database comes up migrated but empty.
	if fresh && o.Project.Dump {
		o.logf("note: the database was restored from the dump and holds no seed data yet,")
		o.logf("      seed it with `wtm exec %s -- <your seed command>`", o.Branch)
	}
	for _, line := range endpoints(o, wt) {
		o.logf("  %s", line)
	}
	return nil
}

// hasCompose reports whether the project ships a docker compose file at all.
func hasCompose(projectDir string) bool {
	_, err := compose.Base(projectDir)
	return err == nil
}

// projectName is the compose project of a worktree stack, which isolates its
// containers, network and volumes from the main stack and from one another.
func (o Options) projectName(wt stack.Worktree) string {
	return stack.ProjectName(filepath.Base(o.Project.Dir), wt.Index, wt.Branch)
}

// resolveIndex fills in the branch's stable index. pos is git's listing
// position, kept only as the resolver's historical fallback.
func (o Options) resolveIndex(ctx context.Context, wt *stack.Worktree, mode index.Mode) error {
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
	if o.Project.Dump {
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
