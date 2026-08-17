// Package stack owns everything about a worktree's docker stack: how it is
// named, which ports it gets, and how it goes up and down.
package stack

import (
	"context"
	"fmt"
	"io"
	"regexp"
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

// Client drives the docker stack of one project's worktrees.
type Client struct {
	Runner execx.Runner
	Dir    string // project repository root
	Out    io.Writer
}

// ProjectName is the compose project wtc gives a worktree. Mirrors its
// composeProjectName/sanitize pair, which is the only way to address that
// stack's containers and volumes from the outside.
func ProjectName(repoName string, index int, branch string) string {
	return sanitize(fmt.Sprintf("%s-wt-%d-%s", repoName, index, branch))
}

// ProjectPrefix is what every compose project of a worktree at this index
// starts with, whatever its branch. It is how leftovers of a removed
// worktree are spotted before the index is handed to a new one.
func ProjectPrefix(repoName string, index int) string {
	return sanitize(fmt.Sprintf("%s-wt-%d", repoName, index)) + "-"
}

func sanitize(input string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(input) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	collapsed := regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-")
	return strings.Trim(collapsed, "-")
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

// Up brings a worktree's stack up. The port assignments live in the .env of
// the worktree, which docker compose reads from the project directory.
func (c *Client) Up(ctx context.Context, project, worktreeDir string, files []string) error {
	args := []string{"compose", "-p", project, "--project-directory", worktreeDir}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "up", "-d", "--build")
	_, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: args,
		Dir:  worktreeDir,
		Env:  []string{"COMPOSE_PROJECT_NAME=" + project},
		Live: true,
	})
	return err
}

// Down stops a worktree's stack, keeping its volumes.
func (c *Client) Down(ctx context.Context, project, worktreeDir string) error {
	_, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "-p", project, "down"},
		Dir:  worktreeDir,
		Live: true,
	})
	return err
}
