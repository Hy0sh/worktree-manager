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
	// then names no branch, so Branch is derived from the path: under
	// WorktreesRoot wtm always creates <root>/<branch>, and elsewhere only the
	// registry's recorded path can say which branch stood there.
	Detached bool
	Head     string
	// UnderRoot says the worktree sits where wtm creates its own. An adopted
	// one does not, which is what Remove reads to leave the directory alone.
	UnderRoot bool
	// Holds is the branch checked out there when it is not Branch: the
	// worktree was switched outside wtm, and Branch is the one its stack is
	// still recorded under. Empty otherwise.
	Holds string
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
// created, plus the adopted ones its registry carries an index for. One
// switched outside wtm is listed under the branch its stack is recorded under.
func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error) {
	all, err := c.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Worktree, 0, len(all))
	for _, wt := range all {
		if recorded := c.RecordedAt(wt); recorded != "" {
			wt.Holds, wt.Branch = wt.Branch, recorded
		}
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
			} else if det {
				wt.Branch = BranchAt(c.Paths, path)
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

// BranchAt is the branch recorded at path in paths (branch to path), empty
// when none is.
func BranchAt(paths map[string]string, path string) string {
	for branch, at := range paths {
		if filepath.Clean(at) == filepath.Clean(path) {
			return branch
		}
	}
	return ""
}

func (c *Client) FindByBranch(ctx context.Context, branch string) (Worktree, error) {
	wts, err := c.Worktrees(ctx)
	if err != nil {
		return Worktree{}, err
	}
	for _, wt := range wts {
		if wt.Branch != branch {
			continue
		}
		switch {
		case c.Out == nil || c.noted[wt.Path]:
		case wt.Detached:
			fmt.Fprintf(c.Out, "note: %s is on a detached HEAD at %s, not on branch %s\n",
				wt.Path, wt.ShortHead(), branch)
		case wt.Holds != "":
			fmt.Fprintf(c.Out, "note: %s now holds %s, this is the stack it had on %s (%s)\n",
				wt.Path, wt.Holds, branch, DriftRemedy(wt))
		}
		// One note per command: start and prepareStack both look the worktree up.
		if c.noted == nil {
			c.noted = map[string]bool{}
		}
		c.noted[wt.Path] = true
		return wt, nil
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

// Manage makes branch's worktree visible to the listing before its index is
// recorded, which an adoption or a switch needs to find the worktree it acts on.
func (c *Client) Manage(branch string) {
	if c.Managed == nil {
		c.Managed = map[string]bool{}
	}
	c.Managed[branch] = true
}

// Rekey files old's recorded path under branch, as config.RekeyWorktree does
// in the registry: this copy would otherwise still list the worktree as old's.
func (c *Client) Rekey(old, branch string) {
	if at, ok := c.Paths[old]; ok {
		delete(c.Paths, old)
		c.Paths[branch] = at
	}
	delete(c.Managed, old)
	c.Manage(branch)
}

// DriftRemedy says how to give a switched worktree's stack to the branch it
// holds. wtm switch refuses a worktree wtm created, named after its branch.
func DriftRemedy(wt Worktree) string {
	if wt.UnderRoot {
		return fmt.Sprintf("switch it back to %s, or `wtm remove %s` then `wtm start %s`",
			wt.Branch, wt.Branch, wt.Holds)
	}
	return fmt.Sprintf("`wtm switch %s` from there moves it to the branch it holds", wt.Holds)
}

// RecordedAt is the branch wt's stack is recorded under when wt now holds
// another branch, "" otherwise. A branch is checked out in one worktree at
// most, so naming the branch it holds names that stack.
func (c *Client) RecordedAt(wt Worktree) string {
	// By path, the stack's real home: a branch can move to another worktree,
	// the directory a stack was started in cannot. Sorted, for a registry that
	// recorded two branches at one path, where wt's own branch wins.
	var recorded []string
	for b, at := range c.Paths {
		if c.Managed[b] && filepath.Clean(at) == filepath.Clean(wt.Path) {
			if b == wt.Branch {
				return ""
			}
			recorded = append(recorded, b)
		}
	}
	if len(recorded) == 0 {
		return ""
	}
	sort.Strings(recorded)
	return recorded[0]
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
