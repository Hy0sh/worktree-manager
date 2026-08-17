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
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

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
	Stack      *stack.Client
	Resolver   *index.Resolver // resolves and records each branch's stable index
}

// dest is where the worktree goes. A branch name is not a safe path fragment:
// `../..` escapes .worktrees entirely, and git only rejects such a refname
// after directories have already been created for it.
func (o Options) dest() (string, error) {
	root := filepath.Join(o.Project.Dir, ".worktrees")
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

// Create builds the worktree and, unless NoStart, brings its stack up.
func Create(ctx context.Context, o Options) error {
	dest, err := o.dest()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("worktree %s already exists, remove it first (`wtm remove %s`)", dest, o.Branch)
	}

	if err := addWorktree(ctx, o, dest); err != nil {
		return err
	}
	if err := provision(ctx, o, dest, overwriteCopies); err != nil {
		return err
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
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	return start(ctx, o, wt.Path)
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
	if err := o.Stack.Down(ctx, o.projectName(wt), wt.Path); err != nil {
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
	stackKnown := false
	if hasCompose(o.Project.Dir) {
		switch err := o.resolveIndex(ctx, &wt, index.MustExist); {
		case errors.Is(err, index.ErrNoIndex):
			o.logf("no stack was ever started for %s: removing the worktree alone", o.Branch)
		case err != nil:
			return err
		default:
			stackKnown = true
			if err := o.Stack.Down(ctx, o.projectName(wt), wt.Path); err != nil {
				return fmt.Errorf("stopping the stack: %w", err)
			}
		}
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
	if stackKnown {
		removeVolumes(ctx, o, wt)
	}
	if err := o.Resolver.Release(o.Branch); err != nil {
		o.logf("warning: the index of %s could not be released: %v", o.Branch, err)
	}
	return nil
}
