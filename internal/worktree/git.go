package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// git runs a git command in the project repository, which always exists where
// a worktree's own directory may not.
func (o Options) git(ctx context.Context, args ...string) (execx.Result, error) {
	return o.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: append([]string{"-C", o.Project.Dir}, args...),
	})
}

// pruneEmptyParents drops the directories a slashed branch name created
// (.worktrees/feat for feat/x), which git leaves behind empty. It stops at the
// first non-empty directory and never touches root itself.
func pruneEmptyParents(path, root string) {
	for dir := filepath.Dir(path); strings.HasPrefix(dir, root+string(os.PathSeparator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

// trackedChanges reports uncommitted changes to tracked files, ignoring the
// untracked artifacts wtm itself put there.
func trackedChanges(ctx context.Context, o Options, wtPath string) (string, error) {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", wtPath, "status", "--porcelain", "--untracked-files=no"},
	})
	if err != nil {
		return "", fmt.Errorf("worktree status: %w", err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// addWorktree checks the branch out into dest: an existing local branch is
// reused, a branch that only exists on a remote is checked out tracking it, and
// an unknown branch is cut from base.
func addWorktree(ctx context.Context, o Options, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
	}
	args := []string{"-C", o.Project.Dir, "worktree", "add"}
	if branchExists(ctx, o) {
		o.logf("branch %s already exists locally: reused as-is, base %q ignored", o.Branch, o.Base)
		args = append(args, dest, o.Branch)
	} else {
		remote, err := remoteBranch(ctx, o)
		if err != nil {
			return err
		}
		if remote == "" {
			noteBaseBehind(ctx, o)
			args = append(args, "-b", o.Branch, dest, o.Base)
		} else {
			o.logf("branch %s only exists on %s: checked out from %s/%s with its upstream set, base %q ignored",
				o.Branch, remote, remote, o.Branch, o.Base)
			args = append(args, "--track", "-b", o.Branch, dest, remote+"/"+o.Branch)
		}
	}
	if _, err := o.Runner.Run(ctx, execx.Cmd{Name: "git", Args: args, Live: true}); err != nil {
		return fmt.Errorf("creating the worktree: %w", err)
	}
	return nil
}

// noteBaseBehind says when the local base the worktree is cut from trails its
// remote; nothing else refreshes it. The cut does not move: someone holding
// unpushed commits on the base would silently get a worktree without them.
func noteBaseBehind(ctx context.Context, o Options) {
	for _, r := range remotes(ctx, o) {
		if !refExists(ctx, o, "refs/remotes/"+r+"/"+o.Base) {
			continue
		}
		// Only the first remote carrying the base is asked. A base living on
		// two remotes is a repository wtm has no business picking sides in.
		_, _ = o.git(ctx, "fetch", "--quiet", r, o.Base)
		res, err := o.git(ctx, "rev-list", "--count", o.Base+".."+r+"/"+o.Base)
		// Offline, behind a VPN, or a base that only exists on the remote: no
		// count, so no gap to claim. A create is not where a network problem
		// gets reported.
		if err != nil {
			return
		}
		if n := strings.TrimSpace(res.Stdout); n != "" && n != "0" {
			o.logf("note: %s is %s commit(s) behind %s/%s, this worktree starts from the local ref",
				o.Base, n, r, o.Base)
		}
		return
	}
}

// remoteBranch returns the single remote that carries o.Branch, or "" when none
// does. Without it, a branch living only on a remote gets cut from base: same
// name, none of its commits, no upstream, a divergence at the first push.
func remoteBranch(ctx context.Context, o Options) (string, error) {
	all := remotes(ctx, o)
	carriers := trackingRefs(ctx, o, all)
	if len(carriers) == 0 {
		// The branch may have been pushed since the last fetch, and only the
		// tracking ref can tell. A branch that exists nowhere makes this fail,
		// which is the ordinary case of creating one, hence the silence.
		for _, r := range all {
			_, _ = o.git(ctx, "fetch", "--quiet", r, o.Branch)
		}
		carriers = trackingRefs(ctx, o, all)
	}
	switch len(carriers) {
	case 0:
		return "", nil
	case 1:
		return carriers[0], nil
	default:
		// This is git's own rule for the same situation: guessing which remote
		// was meant is worse than asking.
		return "", fmt.Errorf("branch %s exists on several remotes (%s): pick one yourself with "+
			"`git branch %s <remote>/%s`, then rerun",
			o.Branch, strings.Join(carriers, ", "), o.Branch, o.Branch)
	}
}

func trackingRefs(ctx context.Context, o Options, all []string) []string {
	var carriers []string
	for _, r := range all {
		if refExists(ctx, o, "refs/remotes/"+r+"/"+o.Branch) {
			carriers = append(carriers, r)
		}
	}
	return carriers
}

func remotes(ctx context.Context, o Options) []string {
	res, err := o.git(ctx, "remote")
	if err != nil {
		return nil
	}
	return strings.Fields(res.Stdout)
}

func branchExists(ctx context.Context, o Options) bool {
	return refExists(ctx, o, "refs/heads/"+o.Branch)
}

func refExists(ctx context.Context, o Options, ref string) bool {
	_, err := o.git(ctx, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// tracked reports whether git follows a file, in which case wtm must not
// rewrite it.
func tracked(ctx context.Context, o Options, dir, name string) bool {
	_, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", dir, "ls-files", "--error-unmatch", name},
	})
	return err == nil
}

func lockReason(wt stack.Worktree) string {
	if wt.LockReason == "" {
		return ""
	}
	return " (" + wt.LockReason + ")"
}
