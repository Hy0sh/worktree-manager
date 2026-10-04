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
	// Paths is the registry's worktree_paths, branch to path. It names the
	// branch of an adopted worktree on a detached HEAD, a rebase stopped on a
	// conflict for one, which git lists with no branch at all.
	Paths map[string]string
}

// Up brings a worktree's stack up, env ("API_PORT=20087") interpolating ahead
// of the worktree's .env. services narrows what starts, empty for all. Naming
// some is additive on a stack already up, so taking a service away needs a stop first.
func (c *Client) Up(ctx context.Context, project, worktreeDir string, files, env, services []string) error {
	args := []string{"compose", "-p", project, "--project-directory", worktreeDir}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "up", "-d", "--build")
	args = append(args, services...)
	_, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: args,
		Dir:  worktreeDir,
		Env:  append([]string{"COMPOSE_PROJECT_NAME=" + project}, env...),
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

// Down takes a worktree's stack away for good, its volumes included. The
// anonymous ones carry no compose label: left by a down, nothing finds them
// again, not even the next up, which mounts fresh ones.
func (c *Client) Down(ctx context.Context, project, worktreeDir string) error {
	return c.compose(ctx, worktreeDir, "-p", project, "down", "--volumes")
}

// Stop halts a worktree's stack and keeps its containers, so the next up
// starts them again with the anonymous volumes they had. A down there left
// those volumes behind on every stop, a batch per stop and start.
func (c *Client) Stop(ctx context.Context, project, worktreeDir string) error {
	return c.compose(ctx, worktreeDir, "-p", project, "stop")
}

func (c *Client) compose(ctx context.Context, dir string, args ...string) error {
	_, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append([]string{"compose"}, args...),
		Dir:  dir,
		Live: true,
	})
	return err
}
