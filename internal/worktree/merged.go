package worktree

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// MergeVerdict says whether `remove --merged` may take a worktree, and why.
type MergeVerdict struct {
	Safe   bool
	Reason string
}

func keep(format string, args ...any) MergeVerdict {
	return MergeVerdict{Reason: fmt.Sprintf(format, args...)}
}

// InspectMerge asks git, never docker, whether wt is clean and a merge commit
// brought its branch into o.Base. Only a merge commit: a branch that never
// diverged sits on the base's first-parent line exactly like a fast-forward.
func InspectMerge(ctx context.Context, o Options, wt stack.Worktree) MergeVerdict {
	switch {
	case wt.Holds != "":
		return keep("switched to %s outside wtm", wt.Holds)
	case wt.Detached:
		return keep("detached HEAD")
	case wt.Locked:
		return keep("locked")
	case wt.Branch == o.Base:
		return keep("it is the base")
	}
	// Untracked files included: remove deletes them without a word, and wtm's
	// own artifacts are in info/exclude, so whatever shows here is somebody's.
	res, err := o.Runner.Run(ctx, execx.Cmd{Name: "git", Args: []string{"-C", wt.Path, "status", "--porcelain"}})
	if err != nil {
		return keep("git status failed")
	}
	// Porcelain v1 puts two status letters and a space before each path.
	if first, _, _ := strings.Cut(strings.TrimRight(res.Stdout, "\n"), "\n"); len(first) > 3 {
		return keep("uncommitted or untracked files (%s)", first[3:])
	}
	refs := []string{o.Base}
	// Nothing is fetched: a merge GitHub made only reaches the tracking ref
	// once something did, and the local base trails it until a pull.
	if res, err := o.git(ctx, "rev-parse", "--abbrev-ref", o.Base+"@{upstream}"); err == nil {
		if up := strings.TrimSpace(res.Stdout); up != "" {
			refs = append(refs, up)
		}
	}
	for _, ref := range refs {
		if mergedInto(ctx, o, wt.Head, ref) {
			return MergeVerdict{Safe: true, Reason: "merged into " + ref}
		}
	}
	res, err = o.git(ctx, "rev-list", "--count", o.Base+".."+wt.Head)
	if err != nil {
		return keep("cannot compare with %s", o.Base)
	}
	if n := strings.TrimSpace(res.Stdout); n != "0" {
		return keep("%s commit(s) not in %s", n, o.Base)
	}
	return keep("on %s's own line (fresh, fast-forwarded or never diverged)", o.Base)
}

// mergedInto says head is in ref but off its first-parent line, which only a
// merge commit puts it. `--not head^@` stops the walk at head's parents, so
// the listing covers what ref gained since, not its whole history.
func mergedInto(ctx context.Context, o Options, head, ref string) bool {
	if _, err := o.git(ctx, "merge-base", "--is-ancestor", head, ref); err != nil {
		return false
	}
	res, err := o.git(ctx, "rev-list", "--first-parent", ref, "--not", head+"^@")
	if err != nil {
		return false
	}
	return !slices.Contains(strings.Fields(res.Stdout), head)
}
