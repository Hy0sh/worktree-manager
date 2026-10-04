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
	"github.com/Hy0sh/worktree-manager/internal/safefile"
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
	// Listed, never dropped: a worktree that vanished leaves stopped containers
	// at most, one that only switched branches leaves them running.
	runningSweep = sweep{noun: "container", list: []string{"ps", "-q"}}
)

// removeLeftovers drops the stack's volumes and built images once the worktree
// is gone. `docker compose down`, which stop runs, keeps both: without this
// every removal leaves its database and gigabytes of images behind forever.
func removeLeftovers(ctx context.Context, o Options, wt stack.Worktree) {
	removeSwept(ctx, o, wt, volumeSweep)
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
	wt, err := prepareStack(ctx, o, dest)
	if err != nil {
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
	if err := o.Stack.Up(ctx, o.projectName(wt), dest, files, portEnv(o, wt), services); err != nil {
		return fmt.Errorf("starting the stack: %w", err)
	}
	if o.Profile != "" {
		o.logf("stack started (worktree %d, %s, profile %s)", wt.Index, o.Branch, o.Profile)
	} else {
		o.logf("stack started (worktree %d, %s)", wt.Index, o.Branch)
	}
	logEndpoints(ctx, o, wt)
	return nil
}

// prepareStack is everything a start writes before compose runs. --no-start
// needs it too: without an index nor these files, a `docker compose up` typed
// in the worktree reaches the stack its compose file names.
func prepareStack(ctx context.Context, o Options, dest string) (stack.Worktree, error) {
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return wt, err
	}
	if err := o.resolveIndex(ctx, &wt, index.MayAllocate); err != nil {
		return wt, err
	}
	if err := ensureSnapshotAssets(o, dest); err != nil {
		return wt, err
	}
	// Without docker the resolver hands out an index it does not record. A stack
	// started on it is still named after it; files written for later are not.
	if o.NoStart && o.Resolver.Recorded()[o.Branch] != wt.Index {
		o.logf("note: docker did not answer, so no index is recorded yet: `wtm start %s` "+
			"writes the ports and the compose override", o.Branch)
		return wt, nil
	}
	if err := allocatePorts(ctx, o, wt, dest); err != nil {
		return wt, err
	}
	return wt, writeNameOverride(o, wt, dest)
}

// writeNameOverride covers the `docker compose` wtm does not run, which loads
// this file when COMPOSE_FILE is unset: its `name:` beats the compose file's,
// and the ports and the dump mount come along.
func writeNameOverride(o Options, wt stack.Worktree, dest string) error {
	own := filepath.Join(dest, compose.OverrideNames[0])
	// Compose loads a single override, the project's own if it has one, and
	// prefers this name: one left from before would shadow the project's.
	for _, name := range compose.OverrideNames {
		path := filepath.Join(dest, name)
		if fileExists(path) && !generatedByWtm(path) {
			if generatedByWtm(own) {
				if err := os.Remove(own); err != nil {
					return err
				}
			}
			o.logf("warning: %s belongs to the project, so a bare `docker compose` in the worktree "+
				"reaches the stack its compose files name: go through `wtm run %s -- docker compose ...`",
				name, o.Branch)
			return nil
		}
	}
	body := fmt.Sprintf("%s Names the worktree's stack, so a bare\n"+
		"# `docker compose` typed here does not reach the main one.\nname: %s\n",
		generatedHeader, o.projectName(wt))
	ports, _ := os.ReadFile(filepath.Join(dest, portsOverride))
	body += withSnapshotMount(o, string(ports))
	if err := safefile.Write(dest, own, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", compose.OverrideNames[0], err)
	}
	// Compose reads both from .env ahead of any compose file: the name then
	// beats this one, and COMPOSE_FILE keeps this file from loading at all.
	if env, err := os.ReadFile(filepath.Join(dest, ".env")); err == nil {
		for _, line := range strings.Split(string(env), "\n") {
			if key, _, ok := strings.Cut(strings.TrimSpace(line), "="); ok &&
				(key == "COMPOSE_PROJECT_NAME" || key == "COMPOSE_FILE") {
				o.logf("warning: .env sets %s, so a bare `docker compose` in the worktree ignores %s: "+
					"go through `wtm run %s -- docker compose ...` or `eval \"$(wtm env)\"`",
					key, compose.OverrideNames[0], o.Branch)
			}
		}
	}
	return nil
}

// mountsSnapshot says the database restores the central dump through a mount.
// A file-based engine has no database service: its dump is copied into the
// worktree, and nothing reads the backup directory at runtime.
func (o Options) mountsSnapshot() bool {
	return o.Project.Dump && !dbengine.IsFileBased(o.Project.BackupConfig().DBEngine)
}

// withSnapshotMount adds the dump mount of .wtm-snapshot.yaml to the ports
// override: a database first brought up without it initialises empty, and
// initdb never runs again on a data directory that is not.
func withSnapshotMount(o Options, ports string) string {
	if !o.mountsSnapshot() {
		return ports
	}
	db := o.Project.BackupConfig().DBService
	mount := "    volumes:\n" + snapshotVolumes()
	// ponytail: splices the text PortsOverride generates, one service per
	// `  name:` line; a real YAML merge if that format ever grows nesting.
	if at := strings.Index(ports, "\n  "+db+":\n"); at >= 0 {
		at += len("\n  " + db + ":\n")
		return ports[:at] + mount + ports[at:]
	}
	if ports == "" {
		ports = "services:\n"
	}
	return ports + "  " + db + ":\n" + mount
}

// generatedHeader opens every compose file wtm writes, and is what tells one it
// may overwrite or remove from a project's own.
const generatedHeader = "# Generated by wtm, do not edit."

func generatedByWtm(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.HasPrefix(string(data), generatedHeader)
}

// logEndpoints lists the addresses of the stack. postCreate prints them a
// second time, since a seed's output buries them.
func logEndpoints(ctx context.Context, o Options, wt stack.Worktree) {
	for _, line := range endpoints(ctx, o, wt) {
		o.logf("  %s", line)
	}
}

// projectName is the compose project of a worktree stack, which isolates its
// containers, network and volumes from the main stack and from one another.
func (o Options) projectName(wt stack.Worktree) string {
	return stack.ProjectName(filepath.Base(o.Project.Dir), wt.Index, wt.Branch)
}

// composeCmd runs compose against a worktree's stack. Dir alone is not enough:
// a wtm called from inside a `wtm run` session inherits that session's
// COMPOSE_FILE, which compose reads ahead of the directory it runs from.
func (o Options) composeCmd(wt stack.Worktree, args ...string) execx.Cmd {
	return execx.Cmd{
		Name: "docker",
		Args: append([]string{"compose", "-p", o.projectName(wt)}, args...),
		Dir:  wt.Path,
		Env:  composeEnv(o, wt),
	}
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
	if o.mountsSnapshot() {
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
