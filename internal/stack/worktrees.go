package stack

import (
	"context"
	"fmt"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Worktree is one linked worktree. Index is the stable, allocated index the
// ports and the compose project name are derived from; this package never
// fills it — the resolver in internal/index does. Pos is merely where git
// listed the worktree, which resorts alphabetically as worktrees come and
// go, so it is only good as a first-guess hint for that resolver.
type Worktree struct {
	Index  int
	Pos    int
	Path   string
	Branch string
}

// Worktrees lists the linked worktrees in `git worktree list --porcelain`
// order, main repository excluded. It records the position as Pos but leaves
// Index to the resolver: git resorts this listing alphabetically, which is
// exactly the instability the persisted index exists to fix.
func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error) {
	res, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", c.Dir, "worktree", "list", "--porcelain"},
	})
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	var (
		out   []Worktree
		first = true
		path  string
		br    string
	)
	flush := func() {
		if path == "" {
			return
		}
		if first {
			first = false // the first block is the main repository
		} else {
			out = append(out, Worktree{Pos: len(out) + 1, Path: path, Branch: br})
		}
		path, br = "", ""
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			br = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return out, nil
}

// FindByBranch locates a branch's worktree (its path, branch and listing
// position); resolving that into an index is the resolver's job, not this
// one's.
func (c *Client) FindByBranch(ctx context.Context, branch string) (Worktree, error) {
	wts, err := c.Worktrees(ctx)
	if err != nil {
		return Worktree{}, err
	}
	for _, wt := range wts {
		if wt.Branch == branch {
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
