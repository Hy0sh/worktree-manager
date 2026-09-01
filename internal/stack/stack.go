// Package stack owns everything about a worktree's docker stack: how it is
// named, which ports it gets, and how it goes up and down.
package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

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
	if err != nil {
		if who := c.portHolder(ctx, err); who != "" {
			return fmt.Errorf("%w\n%s", err, who)
		}
	}
	return err
}

// bindFailed is docker's phrasing when a host port is already published. It
// names the port and never the container holding it.
var bindFailed = regexp.MustCompile(`Bind for \S+:(\d+) failed`)

// portHolder turns a bind failure into the one line that fixes it: which
// container publishes the port. Only docker ps knows, and only a bind failure
// is worth asking it.
func (c *Client) portHolder(ctx context.Context, err error) string {
	var e *execx.Error
	if !errors.As(err, &e) {
		return ""
	}
	m := bindFailed.FindStringSubmatch(e.Stderr)
	if m == nil {
		return ""
	}
	port := m[1]
	res, err := c.Runner.Run(ctx, execx.Cmd{Name: "docker",
		Args: []string{"ps", "--format", "{{.Names}}\t{{.Ports}}"}})
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		name, ports, ok := strings.Cut(line, "\t")
		if ok && strings.Contains(ports, ":"+port+"->") {
			return fmt.Sprintf("port %s is published by container %s", port, name)
		}
	}
	return ""
}

// Down stops a worktree's stack. volumes takes the anonymous ones along too:
// they carry no compose label, so a project naming none leaks one per start.
// stop keeps them, remove takes them.
func (c *Client) Down(ctx context.Context, project, worktreeDir string, volumes bool) error {
	args := []string{"compose", "-p", project, "down"}
	if volumes {
		args = append(args, "--volumes")
	}
	_, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: args,
		Dir:  worktreeDir,
		Live: true,
	})
	return err
}
