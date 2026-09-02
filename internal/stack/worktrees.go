package stack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Worktree carries two very different numbers. Index is the stable one the
// ports and the compose project name derive from, filled by internal/index, not
// here. Pos is where git listed the worktree, which resorts alphabetically, so
// it is only a first-guess hint for that resolver.
type Worktree struct {
	Index  int
	Pos    int
	Path   string
	Branch string
	// git refuses to remove a locked worktree even with one --force. A lock can
	// carry no reason, so the flag cannot be inferred from LockReason.
	Locked     bool
	LockReason string
	// Detached says HEAD points straight at Head instead of at a branch. git
	// then names no branch, so Branch is derived from the path, which only
	// works under WorktreesRoot: wtm always creates <root>/<branch>.
	Detached bool
	Head     string
	// UnderRoot says the worktree sits where wtm creates its own. An adopted
	// one does not, which is what Remove reads to leave the directory alone.
	UnderRoot bool
}

// ShortHead abbreviates Head the way git prints it in its own listings.
func (w Worktree) ShortHead() string {
	if len(w.Head) > 8 {
		return w.Head[:8]
	}
	return w.Head
}

// WorktreesRoot is where the worktrees wtm creates itself live. What git lists
// outside it, a `claude -w` worktree under .claude/worktrees or a manual `git
// worktree add` anywhere else, has none of what every wtm command needs until
// `wtm adopt` gives it a stable index, a provisioned .env and a stack.
func WorktreesRoot(repoDir string) string {
	return filepath.Join(repoDir, ".worktrees")
}

// Worktrees lists the worktrees wtm manages, in git's own order: the ones it
// created, plus the adopted ones its registry carries an index for.
func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error) {
	all, err := c.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Worktree, 0, len(all))
	for _, wt := range all {
		if wt.UnderRoot || c.Managed[wt.Branch] {
			out = append(out, wt)
		}
	}
	return out, nil
}

// All lists every linked worktree of the repository, whether wtm knows it or
// not. `wtm adopt` is what it exists for: a worktree has to be seen once before
// it can be brought in, and Worktrees hides exactly the ones worth adopting.
func (c *Client) All(ctx context.Context) ([]Worktree, error) {
	res, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", c.Dir, "worktree", "list", "--porcelain"},
	})
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	root := WorktreesRoot(c.Dir) + string(os.PathSeparator)
	var (
		out []Worktree
		// pos counts the worktrees under root alone. It is the fallback
		// internal/index uses for worktrees older than the recorded indices, so
		// it must keep meaning "nth under .worktrees": numbering adopted ones
		// along with them would shift that fallback and hand such a worktree an
		// index its .env never carried.
		pos    int
		first  = true
		path   string
		br     string
		locked bool
		reason string
		det    bool
		head   string
	)
	flush := func() {
		if path == "" {
			return
		}
		switch {
		case first:
			first = false // the first block is the main repository
		default:
			wt := Worktree{Path: path, Branch: br, Locked: locked,
				LockReason: reason, Detached: det, Head: head}
			if wt.UnderRoot = strings.HasPrefix(path, root); wt.UnderRoot {
				if det {
					wt.Branch = filepath.ToSlash(strings.TrimPrefix(path, root))
				}
				pos++
				wt.Pos = pos
			}
			out = append(out, wt)
		}
		path, br, locked, reason, det, head = "", "", false, "", false, ""
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			head = strings.TrimPrefix(line, "HEAD ")
		case line == "detached":
			det = true
		case strings.HasPrefix(line, "branch "):
			br = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "locked":
			locked = true
		case strings.HasPrefix(line, "locked "):
			locked, reason = true, strings.TrimPrefix(line, "locked ")
		}
	}
	flush()
	return out, nil
}

func (c *Client) FindByBranch(ctx context.Context, branch string) (Worktree, error) {
	wts, err := c.Worktrees(ctx)
	if err != nil {
		return Worktree{}, err
	}
	for _, wt := range wts {
		if wt.Branch == branch {
			if wt.Detached && c.Out != nil {
				fmt.Fprintf(c.Out, "note: %s is on a detached HEAD at %s, not on branch %s\n",
					wt.Path, wt.ShortHead(), branch)
			}
			return wt, nil
		}
	}
	known := make([]string, 0, len(wts))
	for _, wt := range wts {
		known = append(known, fmt.Sprintf("%d:%s", wt.Pos, wt.Branch))
	}
	list := "no linked worktree"
	if len(known) > 0 {
		list = "known worktrees: " + strings.Join(known, ", ")
	}
	return Worktree{}, fmt.Errorf("no worktree for branch %q (%s)", branch, list)
}
