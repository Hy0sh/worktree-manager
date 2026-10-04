package worktree

import (
	"context"
	"fmt"
	"slices"

	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// RemovalKind says what a removal takes away, which one verb hides: an adopted
// worktree keeps its directory, a vanished one has none left to take.
type RemovalKind int

const (
	// RemoveCreated takes the stack, its volumes and the directory wtm created.
	RemoveCreated RemovalKind = iota
	// RemoveAdopted takes the stack and wtm's own files, and leaves the checkout.
	RemoveAdopted
	// RemoveStale releases the index of a worktree that left outside wtm,
	// taking down whatever stack still stands at it.
	RemoveStale
	// RemoveAbandoned deletes a directory git no longer lists as a worktree.
	RemoveAbandoned
)

// RemovalPlan is what Remove would do with the same Options, found without
// changing anything: a caller can show it before asking, and Remove acts on it,
// so what calls for --force is decided in one place.
type RemovalPlan struct {
	Kind RemovalKind
	// Path is the worktree, or the directory git forgot. Empty for RemoveStale.
	Path string
	// Index is the one the registry records, 0 when it holds none.
	Index      int
	Locked     bool
	LockReason string
	// Changes are the tracked changes of a created worktree, left unchecked
	// under Force: skipping that check is what --force is for.
	Changes string
	wt      stack.Worktree
}

// RequiresForce says Remove refuses the plan unless Options.Force is set.
func (p RemovalPlan) RequiresForce() bool {
	switch p.Kind {
	case RemoveAbandoned:
		return true
	case RemoveCreated:
		return p.Locked || p.Changes != ""
	}
	return false
}

// InspectRemoval asks git and the registry, never docker, and writes nothing.
// A branch with neither a worktree nor an index nor a directory left gets git's
// own answer, which names the worktrees that do exist.
func InspectRemoval(ctx context.Context, o Options) (RemovalPlan, error) {
	recorded := o.Resolver.Recorded()[o.Branch]
	wt, listErr := o.Stack.FindByBranch(ctx, o.Branch)
	if listErr != nil {
		if recorded > 0 {
			return RemovalPlan{Kind: RemoveStale, Index: recorded}, nil
		}
		dest, err := o.dest()
		if err != nil {
			return RemovalPlan{}, listErr
		}
		// Stack.Abandoned tells a directory git forgot from a live worktree git
		// now lists under a renamed branch, which must not be deleted.
		abandoned, err := o.Stack.Abandoned(ctx)
		if err != nil || !slices.Contains(abandoned, dest) {
			return RemovalPlan{}, listErr
		}
		return RemovalPlan{Kind: RemoveAbandoned, Path: dest}, nil
	}
	plan := RemovalPlan{Kind: RemoveAdopted, Path: wt.Path, Index: recorded, wt: wt}
	// An adopted checkout stays where it is, so nothing in it is at risk. So
	// does one switched outside wtm: it holds another branch's work now.
	if !wt.UnderRoot || wt.Holds != "" {
		return plan, nil
	}
	plan.Kind = RemoveCreated
	plan.Locked, plan.LockReason = wt.Locked, wt.LockReason
	if o.Force {
		return plan, nil
	}
	changes, err := trackedChanges(ctx, o, wt.Path)
	if err != nil {
		return RemovalPlan{}, err
	}
	plan.Changes = changes
	return plan, nil
}

// refusal is the error Remove stops on before taking anything down, nil when
// the plan can go ahead as o stands.
func (p RemovalPlan) refusal(o Options) error {
	if o.Force || !p.RequiresForce() {
		return nil
	}
	switch {
	case p.Kind == RemoveAbandoned:
		// git's metadata is what `git status` reads, so no check can say
		// whether the directory holds uncommitted work.
		return fmt.Errorf("%s is a directory git no longer lists as a worktree: its administrative "+
			"directory is gone, so nothing can say whether it holds uncommitted work.\n"+
			"rerun with --force to delete it", p.Path)
	case p.Locked:
		return fmt.Errorf("worktree %s is locked%s: unlock it (`git -C %s worktree unlock %s`), "+
			"or rerun with --force (the stack is still running)",
			p.Path, lockReason(p.wt), o.Project.Dir, p.Path)
	default:
		return fmt.Errorf("worktree %s has uncommitted changes:\n%s\ncommit them, or rerun with --force "+
			"(the stack is still running)", p.Path, p.Changes)
	}
}
