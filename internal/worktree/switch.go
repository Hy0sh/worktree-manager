package worktree

import (
	"context"
	"fmt"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// Switch moves the adopted worktree of the current directory to o.Branch and
// gives it a fresh stack, keeping its index and so its ports. Git goes first: a
// refused checkout leaves the old stack untouched. Rerunning finishes a switch
// that failed halfway, since the recorded path still names the old branch.
func Switch(ctx context.Context, o Options) error {
	if err := refuseBadBranch(o); err != nil {
		return err
	}
	wt, cur, err := currentWorktree(ctx, o, "run switch from the worktree to move")
	if err != nil {
		return err
	}
	switch {
	case wt.UnderRoot:
		// Its directory is named after its branch: switching would leave a
		// worktree whose path lies about what it holds.
		return fmt.Errorf("%s is a worktree wtm created, named after its branch: "+
			"create one for %s with `wtm create %s`", wt.Path, o.Branch, o.Branch)
	}
	recorded := o.Resolver.Recorded()
	old := stack.BranchAt(o.Stack.Paths, wt.Path)
	if old == "" && recorded[cur.Branch] > 0 {
		old = cur.Branch // adopted before paths were recorded
	}
	if old == "" {
		return fmt.Errorf("%s has no stack to switch: adopt it first with `wtm adopt`", wt.Path)
	}
	if n := recorded[o.Branch]; n > 0 && old != o.Branch {
		return fmt.Errorf("branch %s already has a stack at index %d: nothing was changed", o.Branch, n)
	}

	if cur.Branch != o.Branch {
		changes, err := trackedChanges(ctx, o, wt.Path)
		if err != nil {
			return err
		}
		if changes != "" {
			return fmt.Errorf("%s has uncommitted changes to tracked files, commit or stash them "+
				"first: nothing was changed\n%s", wt.Path, changes)
		}
		if err := checkout(ctx, o, wt.Path); err != nil {
			return err
		}
	}

	if old != o.Branch {
		if n := recorded[old]; n > 0 && compose.Has(o.Project.Dir) {
			prev := stack.Worktree{Index: n, Branch: old, Path: wt.Path}
			if err := o.Stack.Down(ctx, o.projectName(prev), wt.Path); err != nil {
				return fmt.Errorf("taking down the stack of %s (now on %s, rerun the same command): %w",
					old, o.Branch, err)
			}
			removeLeftovers(ctx, o, prev)
		}
		if err := config.RekeyWorktree(o.Resolver.ConfigPath, o.Name, old, o.Branch); err != nil {
			return fmt.Errorf("moving the index of %s to %s (rerun the same command): %w", old, o.Branch, err)
		}
		o.logf("stack of %s dropped, its index now belongs to %s", old, o.Branch)
	}

	o.Stack.Manage(o.Branch)
	return provisionAndStart(ctx, o, wt.Path, keepWorktreeCopies)
}

// checkout puts o.Branch in dir with the rules of addWorktree (startPoint).
func checkout(ctx context.Context, o Options, dir string) error {
	flags, ref, err := startPoint(ctx, o, "-c")
	if err != nil {
		return err
	}
	args := append(append([]string{"-C", dir, "switch"}, flags...), ref)
	if _, err := o.Runner.Run(ctx, execx.Cmd{Name: "git", Args: args, Live: true}); err != nil {
		return fmt.Errorf("switching to %s (nothing else was changed): %w", o.Branch, err)
	}
	return nil
}
