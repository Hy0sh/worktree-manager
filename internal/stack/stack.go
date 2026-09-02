// Package stack owns everything about a worktree's docker stack: how it is
// named, which ports it gets, and how it goes up and down.
package stack

import (
	"context"
	"io"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

type Client struct {
	Runner execx.Runner
	Dir    string // project repository root
	Out    io.Writer
	// Managed carries the branches holding a recorded index, which is what
	// makes a worktree outside .worktrees visible to Worktrees. Nothing in such
	// a worktree's path tells it apart from a stranger's.
	Managed map[string]bool
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
