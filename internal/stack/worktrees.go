package stack

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Worktree carries two very different numbers. Index is the stable one ports
// and the compose project name derive from, filled by internal/index. Pos is
// where git listed it, which resorts alphabetically: a hint for that resolver.
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
// outside it, `claude -w` worktrees or a manual `git worktree add`, has no
// index, no provisioned .env and no stack until `wtm adopt` gives it those.
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
		// pos counts the worktrees under root alone: it is internal/index's
		// fallback for worktrees older than recorded indices, so numbering an
		// adopted one would hand it an index its .env never carried.
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

// Abandoned lists the directories under WorktreesRoot that still carry a
// worktree's .git pointer file while `git worktree list` no longer names them:
// what a pruned or hand-deleted administrative directory leaves on disk. Every
// other command keys off git's listing, so nothing else can see them.
func (c *Client) Abandoned(ctx context.Context) ([]string, error) {
	root := WorktreesRoot(c.Dir)
	if _, err := os.Stat(root); err != nil {
		return nil, nil
	}
	all, err := c.All(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(all))
	for _, wt := range all {
		known[filepath.Clean(wt.Path)] = true
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if _, err := os.Lstat(filepath.Join(path, ".git")); err != nil {
			// A slashed branch name nests, so a directory without .git is a
			// parent to walk into and not an answer.
			return nil
		}
		if !known[filepath.Clean(path)] {
			out = append(out, path)
		}
		return fs.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, err)
	}
	sort.Strings(out)
	return out, nil
}
