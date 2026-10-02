package worktree

import (
	"context"
	"fmt"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// Switch moves the adopted worktree of the current directory to o.Branch and
// gives it a fresh stack, keeping its index and so its ports. Git goes first: a
// refused checkout leaves the old stack untouched. Rerunning finishes a switch
// that failed halfway, since the recorded path still names the old branch.
func Switch(ctx context.Context, o Options) error {
	if err := refuseOptionLike(o.Branch, o.Base); err != nil {
		return err
	}
	if err := refuseRefspec(o.Branch); err != nil {
		return err
	}
	cur, err := gitx.CurrentWorktree(ctx, o.Runner)
	if err != nil {
		return err
	}
	if !cur.Linked {
		return fmt.Errorf("%s is the repository itself and not a worktree: "+
			"run switch from the worktree to move", cur.Path)
	}
	all, err := o.Stack.All(ctx)
	if err != nil {
		return err
	}
	var wt stack.Worktree
	for _, w := range all {
		if config.SamePath(w.Path, cur.Path) {
			wt = w
		}
	}
	switch {
	case wt.Path == "":
		return fmt.Errorf("%s is a worktree of another repository than %s", cur.Path, o.Project.Dir)
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
			if err := o.Stack.Down(ctx, o.projectName(prev), wt.Path, true); err != nil {
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

	if o.Stack.Managed == nil {
		o.Stack.Managed = map[string]bool{}
	}
	o.Stack.Managed[o.Branch] = true
	return provisionAndStart(ctx, o, wt.Path, keepWorktreeCopies)
}

// checkout puts o.Branch in dir with the rules of addWorktree: a local branch
// as-is, a branch only a remote carries tracking it, anything else cut from base.
func checkout(ctx context.Context, o Options, dir string) error {
	args := []string{"-C", dir, "switch"}
	if branchExists(ctx, o) {
		o.logf("branch %s already exists locally: checked out as-is, base %q ignored", o.Branch, o.Base)
		args = append(args, o.Branch)
	} else {
		remote, err := remoteBranch(ctx, o)
		if err != nil {
			return err
		}
		if remote == "" {
			noteBaseBehind(ctx, o)
			args = append(args, "-c", o.Branch, o.Base)
		} else {
			o.logf("branch %s only exists on %s: checked out from %s/%s with its upstream set, base %q ignored",
				o.Branch, remote, remote, o.Branch, o.Base)
			args = append(args, "--track", "-c", o.Branch, remote+"/"+o.Branch)
		}
	}
	if _, err := o.Runner.Run(ctx, execx.Cmd{Name: "git", Args: args, Live: true}); err != nil {
		return fmt.Errorf("switching to %s (nothing else was changed): %w", o.Branch, err)
	}
	return nil
}
