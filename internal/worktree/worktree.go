// Package worktree creates, starts, stops and removes project worktrees, and
// drives the docker stack that goes with each one.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

type Options struct {
	Name         string // project name in the registry
	Project      config.Project
	Branch       string
	Base         string
	NoStart      bool
	NoPostCreate bool // skip the project's post_create on this create
	// RunAfter and ExecAfter are the commands of `create --run` and
	// `create --exec`, played once the create is done: the first on the host
	// from the worktree, the second in the application container. Both are
	// shell lines and never persisted, unlike post_create.
	RunAfter   string
	ExecAfter  string
	Force      bool // remove despite uncommitted tracked changes
	BackupsDir string
	Runner     execx.Runner
	Out        io.Writer
	Stack      *stack.Client
	Resolver   *index.Resolver // resolves and records each branch's stable index
	// Confirm asks a yes-or-no question, and is nil when nobody is there to
	// answer. The memory advisory is its only caller: it rests on an average
	// over the running stacks, which predicts neither a light stack nor a heavy
	// one, so it is worth a question to a person and never worth failing the
	// create of a script or an agent.
	Confirm func(question string) bool
	// BaseFromHere says Base came from `create --from-here` rather than from
	// the project or the command line. An existing branch is checked out as-is
	// and ignores the base, so the flag would quietly do nothing there, and
	// that combination is refused rather than logged.
	BaseFromHere bool
	// RenameTo is the name an adopted branch takes. Adoption is the only
	// moment a rename is free: the compose project name carries the branch, so
	// renaming once a stack exists orphans the stack it names.
	RenameTo string
	// ConfirmAdopt asks before writing into a worktree wtm did not create. It
	// is deliberately not Confirm: --ignore-memory answers the memory advisory
	// alone, and must not also wave through a write into somebody's checkout.
	ConfirmAdopt func(question string) bool
}

// errStackNotStarted says a person declined to bring the stack up. The worktree
// is created and usable, so nothing above treats it as a failure.
var errStackNotStarted = errors.New("stack not started")

// dest is where the worktree goes. A branch name is not a safe path fragment:
// `../..` escapes .worktrees entirely, and git only rejects such a refname
// after directories have already been created for it.
func (o Options) dest() (string, error) {
	root := stack.WorktreesRoot(o.Project.Dir)
	dest := filepath.Join(root, o.Branch)
	if dest != root && !strings.HasPrefix(dest, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid branch name %q: the worktree would land outside %s", o.Branch, root)
	}
	if dest == root {
		return "", fmt.Errorf("invalid branch name %q", o.Branch)
	}
	return dest, nil
}

func (o Options) logf(format string, args ...any) {
	if o.Out != nil {
		fmt.Fprintf(o.Out, format+"\n", args...)
	}
}

func Create(ctx context.Context, o Options) error {
	dest, err := o.dest()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("worktree %s already exists, remove it first (`wtm remove %s`)", dest, o.Branch)
	}
	if o.BaseFromHere && branchExists(ctx, o) {
		return fmt.Errorf("branch %s already exists, so --from-here would be ignored: "+
			"drop the flag and it is checked out as-is", o.Branch)
	}

	if err := addWorktree(ctx, o, dest); err != nil {
		return err
	}
	return provisionAndStart(ctx, o, dest, overwriteCopies)
}

// provisionAndStart is everything a create does past the checkout itself, which
// is exactly what Adopt owes a worktree it did not check out.
func provisionAndStart(ctx context.Context, o Options, dest string, mode provisionMode) error {
	if err := provision(ctx, o, dest, mode); err != nil {
		return err
	}
	if err := ensureSnapshotAssets(o, dest); err != nil {
		return err
	}
	o.logf("worktree ready: %s", dest)

	if o.NoStart {
		o.logf("stack not started (--no-start), bring it up with `wtm start %s`", o.Branch)
		runAfter(ctx, o)
		return nil
	}
	if err := start(ctx, o, dest); err != nil {
		if errors.Is(err, errStackNotStarted) {
			// afterCreate is what plays post_create and --exec, and this path
			// never reaches it. `wtm start` does not replay post_create either,
			// so the lines that would play them are named here or nowhere.
			post := o.Project.PostCreate
			if o.NoPostCreate {
				post = ""
			}
			replayLines(o, "run", post, o.ExecAfter)
			// Nothing in a container can run, but a command working on the
			// files is exactly what a worktree without a stack is good for.
			o.NoStart = true
			runAfter(ctx, o)
			return nil
		}
		return err
	}
	afterCreate(ctx, o)
	// Last of all: the command takes the terminal, so everything worth reading
	// has to be on screen before it does.
	runAfter(ctx, o)
	return nil
}

// Adopt gives a worktree wtm did not create everything it gives one it did: a
// stable index, provisioned .env files and compose overrides, a restored
// snapshot and a stack. The worktree stays where it is, because the whole point
// is a directory somebody else opened, a `claude -w` one at the head of the
// list, possibly with an agent working in it right now.
func Adopt(ctx context.Context, o Options) error {
	wt, err := adoptTarget(ctx, &o)
	if err != nil {
		return err
	}
	switch {
	case wt.UnderRoot:
		return fmt.Errorf("%s is a worktree wtm created: start its stack with `wtm start %s`",
			wt.Path, wt.Branch)
	case wt.Detached:
		return fmt.Errorf("%s is on a detached HEAD at %s, and wtm keys a worktree by its branch: "+
			"check one out there (`git -C %s switch -c <branch>`), then rerun",
			wt.Path, wt.ShortHead(), wt.Path)
	case o.Resolver.Recorded()[wt.Branch] > 0:
		return fmt.Errorf("%s is already adopted: start its stack with `wtm start %s`",
			wt.Path, wt.Branch)
	}
	if o.RenameTo != "" && refExists(ctx, o, "refs/heads/"+o.RenameTo) {
		return fmt.Errorf("branch %s already exists: pick another name for %s",
			o.RenameTo, wt.Branch)
	}
	question := fmt.Sprintf(
		"adopt %s? wtm writes its .env, its compose overrides and its own files there", wt.Path)
	if o.RenameTo != "" {
		question = fmt.Sprintf("adopt %s as %s? wtm renames the branch, then writes "+
			"its .env, its compose overrides and its own files there", wt.Path, o.RenameTo)
	}
	if o.ConfirmAdopt != nil && !o.ConfirmAdopt(question) {
		return errors.New("cancelled: nothing was written (pass -y to answer for a script)")
	}
	if o.RenameTo != "" {
		if _, err := o.Runner.Run(ctx, execx.Cmd{
			Name: "git",
			Args: []string{"-C", o.Project.Dir, "branch", "-m", wt.Branch, o.RenameTo},
		}); err != nil {
			return fmt.Errorf("renaming %s to %s: %w", wt.Branch, o.RenameTo, err)
		}
		// git moves the worktree's HEAD along with the branch, so everything
		// below, the index included, belongs to the new name from here.
		o.logf("branch %s renamed to %s", wt.Branch, o.RenameTo)
		wt.Branch, o.Branch = o.RenameTo, o.RenameTo
	}
	// The index is what every other command reads to see this worktree at all,
	// and start allocates it. Until it does, the listing still hides the
	// worktree, so FindByBranch would fail on the very thing being adopted.
	if o.Stack.Managed == nil {
		o.Stack.Managed = map[string]bool{}
	}
	o.Stack.Managed[wt.Branch] = true
	// Allocated here and not left to start: --no-start never reaches it, and an
	// adoption that records no index is one no later command can see.
	if err := o.resolveIndex(ctx, &wt, index.MayAllocate); err != nil {
		return err
	}
	return provisionAndStart(ctx, o, wt.Path, keepWorktreeCopies)
}

// adoptTarget resolves what to adopt and fills o.Branch with it: the named
// branch, or the worktree the command was typed from. It looks through All and
// not Worktrees, which hides exactly the worktrees worth adopting.
func adoptTarget(ctx context.Context, o *Options) (stack.Worktree, error) {
	all, err := o.Stack.All(ctx)
	if err != nil {
		return stack.Worktree{}, err
	}
	if o.Branch != "" {
		for _, wt := range all {
			if wt.Branch == o.Branch {
				return wt, nil
			}
		}
		return stack.Worktree{}, fmt.Errorf("no worktree for branch %q in %s: "+
			"create one with `wtm create %s`", o.Branch, o.Project.Dir, o.Branch)
	}
	cur, err := gitx.CurrentWorktree(ctx, o.Runner)
	if err != nil {
		return stack.Worktree{}, err
	}
	if !cur.Linked {
		return stack.Worktree{}, fmt.Errorf("%s is the repository itself and not a worktree: "+
			"name a branch, or run this from the worktree to adopt", cur.Path)
	}
	for _, wt := range all {
		if sameDir(wt.Path, cur.Path) {
			o.Branch = wt.Branch
			return wt, nil
		}
	}
	return stack.Worktree{}, fmt.Errorf("%s is a worktree of another repository than %s",
		cur.Path, o.Project.Dir)
}

// removeArtifacts takes wtm's own files back out of a checkout it never owned.
// Only what it can prove is its own: the two generated compose files, the two
// symlinks, and the delimited port block. A copied .env or compose override is
// indistinguishable from a file the worktree already had, and a file-based
// database is data, so both stay. None of this unblocks anything, git already
// tolerates them all through info/exclude; it is about not leaving a dead port
// block behind in a worktree that outlives its stack.
func removeArtifacts(o Options, dest string) {
	for _, name := range []string{portsOverride, snapshotOverride, snapshotLink, gitContainerLink} {
		if err := os.Remove(filepath.Join(dest, name)); err != nil && !os.IsNotExist(err) {
			o.logf("warning: %s could not be removed from %s: %v", name, dest, err)
		}
	}
	if err := stack.StripEnvOverrides(dest); err != nil {
		o.logf("warning: the port block could not be taken out of %s/.env: %v", dest, err)
	}
}

// sameDir compares two paths git and the shell can spell differently: macOS
// hands out symlinked temporary directories, and only the resolved forms match.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// Start brings an existing worktree's stack back up. Without it, restarting a
// stopped worktree means calling docker compose with the index wtm derives,
// which is
// exactly the internal knowledge this tool exists to hide.
func Start(ctx context.Context, o Options) error {
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	// A declined start already said so on its own: the caller has nothing to add.
	if err := start(ctx, o, wt.Path); err != nil && !errors.Is(err, errStackNotStarted) {
		return err
	}
	return nil
}

// Stop takes the stack down and leaves the worktree in place.
func Stop(ctx context.Context, o Options) error {
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	if !hasCompose(o.Project.Dir) {
		o.logf("no compose file in this project: no stack to stop")
		return nil
	}
	if err := o.resolveIndex(ctx, &wt, index.MustExist); err != nil {
		if errors.Is(err, index.ErrNoIndex) {
			o.logf("no stack was ever started for %s: nothing to stop", o.Branch)
			return nil
		}
		return err
	}
	if err := o.Stack.Down(ctx, o.projectName(wt), wt.Path, false); err != nil {
		return fmt.Errorf("stopping the stack: %w", err)
	}
	o.logf("stack stopped (worktree %d, %s)", wt.Index, o.Branch)
	return nil
}

// Remove stops the stack then removes the worktree, keeping the local branch.
func Remove(ctx context.Context, o Options) error {
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	// Checked before anything is taken down: a refusal must leave the worktree
	// exactly as it was, stack included. Only tracked changes are work worth
	// protecting; git refuses to remove a worktree holding any untracked file,
	// which this tool always put there, so the removal below forces past those.
	// An adopted worktree is spared the whole question: its checkout stays, so
	// there is nothing to protect it from, and refusing over the uncommitted
	// work a worktree in use always holds would make the command unusable.
	if !o.Force && wt.UnderRoot {
		if wt.Locked {
			return fmt.Errorf("worktree %s is locked%s: unlock it (`git -C %s worktree unlock %s`), "+
				"or rerun with --force (the stack is still running)",
				wt.Path, lockReason(wt), o.Project.Dir, wt.Path)
		}
		changes, err := trackedChanges(ctx, o, wt.Path)
		if err != nil {
			return err
		}
		if changes != "" {
			return fmt.Errorf("worktree %s has uncommitted changes:\n%s\ncommit them, or rerun with --force "+
				"(the stack is still running)", wt.Path, changes)
		}
	}

	stackKnown := false
	if hasCompose(o.Project.Dir) {
		switch err := o.resolveIndex(ctx, &wt, index.MustExist); {
		case errors.Is(err, index.ErrNoIndex):
			o.logf("no stack was ever started for %s: removing the worktree alone", o.Branch)
		case err != nil:
			return err
		default:
			stackKnown = true
			if err := o.Stack.Down(ctx, o.projectName(wt), wt.Path, true); err != nil {
				return fmt.Errorf("stopping the stack: %w", err)
			}
		}
	}
	if wt.UnderRoot {
		// The first --force covers the untracked files wtm itself put there; a
		// lock takes a second one, which is git's own rule.
		args := []string{"-C", o.Project.Dir, "worktree", "remove", "--force"}
		if wt.Locked {
			args = append(args, "--force")
		}
		if _, err := o.Runner.Run(ctx, execx.Cmd{
			Name: "git",
			Args: append(args, wt.Path),
			Live: true,
		}); err != nil {
			return fmt.Errorf("removing the worktree (the stack is already stopped): %w", err)
		}
		pruneEmptyParents(wt.Path, stack.WorktreesRoot(o.Project.Dir))
		o.logf("worktree removed: %s (branch %s kept)", wt.Path, o.Branch)
	} else {
		// wtm only takes down what it built. The checkout came from somewhere
		// else, and something may well still be working in it.
		removeArtifacts(o, wt.Path)
		o.logf("stack removed, worktree kept: %s (wtm did not create it)", wt.Path)
	}
	if stackKnown {
		removeVolumes(ctx, o, wt)
		removeImages(ctx, o, wt)
	}
	if err := o.Resolver.Release(o.Branch); err != nil {
		o.logf("warning: the index of %s could not be released: %v", o.Branch, err)
	}
	return nil
}
