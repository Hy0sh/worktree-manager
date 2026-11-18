// Package stack owns everything about a worktree's docker stack: how it is
// named, which ports it gets, and how it goes up and down.
package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/Hy0sh/worktree-manager/internal/config"
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
	noted map[string]bool // worktrees FindByBranch already wrote a note about
}

// Up brings a worktree's stack up, env ("API_PORT=20087") interpolating ahead
// of the worktree's .env. services narrows what starts, empty for all. Naming
// some is additive on a stack already up, so taking a service away needs a stop first.
func (c *Client) Up(ctx context.Context, project, worktreeDir string, files, env, services []string) error {
	args := []string{"compose", "-p", project, "--project-directory", worktreeDir}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	env = append([]string{"COMPOSE_PROJECT_NAME=" + project}, env...)
	if err := c.createExternalVolumes(ctx, worktreeDir, args, env); err != nil {
		return err
	}
	args = append(args, "up", "-d", "--build")
	args = append(args, services...)
	_, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: args,
		Dir:  worktreeDir,
		Env:  env,
		Live: true,
	})
	if err != nil {
		if who := c.portHolder(ctx, err); who != "" {
			return fmt.Errorf("%w\n%s", err, who)
		}
	}
	return err
}

// createExternalVolumes creates the `external: true` volumes docker lacks.
// compose never removes them on `down --volumes` yet refuses to start without
// them. Docker seeds an empty one from the image on first mount, a copy that
// takes minutes on a big node_modules, during which another stack mounting it
// sees a partial tree: a fresh volume is seeded here, under a lock every wtm shares.
func (c *Client) createExternalVolumes(ctx context.Context, dir string, base, env []string) error {
	res, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append(slices.Clone(base), "config", "--format", "json"),
		Dir:  dir,
		Env:  env,
	})
	// Unreadable config: skip, `up` reports the real error better than this would.
	type mount struct {
		Type   string `json:"type"`
		Source string `json:"source"` // the volume's key in the compose file
	}
	type volume struct {
		Name     string `json:"name"`
		External bool   `json:"external"`
	}
	var cfg struct {
		Services map[string]struct {
			Volumes []mount `json:"volumes"`
		} `json:"services"`
		Volumes map[string]volume `json:"volumes"`
	}
	if err != nil || json.Unmarshal([]byte(res.Stdout), &cfg) != nil {
		return nil
	}
	shared := func(v volume) bool { return v.External && v.Name != "" }
	if !slices.ContainsFunc(slices.Collect(maps.Values(cfg.Volumes)), shared) {
		return nil
	}
	unlock, err := c.lockSharedVolumes(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// A file per volume being seeded: one left behind means a wtm was killed
	// mid-copy, and docker never fills a non-empty volume again.
	cfgDir, err := config.Dir()
	if err != nil {
		return err
	}
	pendingDir := filepath.Join(cfgDir, "seeding")
	if err := os.MkdirAll(pendingDir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", pendingDir, err)
	}

	key := ""
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "WTM_DEPS_KEY="); ok {
			key = v
		}
	}
	var fresh []string           // docker names of the volumes created here
	seeders := map[string]bool{} // services mounting one of them
	for _, k := range slices.Sorted(maps.Keys(cfg.Volumes)) {
		v := cfg.Volumes[k]
		if !shared(v) {
			continue
		}
		pending := filepath.Join(pendingDir, v.Name)
		if _, err := os.Stat(pending); err == nil {
			fmt.Fprintf(c.Out, "dropping %s, left half-filled by an interrupted seeding\n", v.Name)
			if _, err := c.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"volume", "rm", "-f", v.Name}}); err != nil {
				return fmt.Errorf("dropping the half-filled volume %s, stop the stacks mounting it first: %w", v.Name, err)
			}
		} else if _, err := c.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"volume", "inspect", v.Name}}); err == nil {
			continue
		}
		if err := os.WriteFile(pending, nil, 0o600); err != nil {
			return err
		}
		args := []string{"volume", "create"}
		// Only a name carrying the key is a dependency volume, the only kind
		// wtm may later garbage-collect.
		if key != "" && strings.Contains(v.Name, key) {
			args = append(args, "--label", "wtm.deps-key="+key)
		}
		if _, err := c.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: append(args, v.Name)}); err != nil {
			return fmt.Errorf("creating the shared volume %s: %w", v.Name, err)
		}
		fresh = append(fresh, v.Name)
		// One service per volume is enough: the first mount fills it.
		for _, s := range slices.Sorted(maps.Keys(cfg.Services)) {
			if slices.Contains(cfg.Services[s].Volumes, mount{Type: "volume", Source: k}) {
				seeders[s] = true
				break
			}
		}
	}
	for _, s := range slices.Sorted(maps.Keys(seeders)) {
		fmt.Fprintf(c.Out, "seeding the shared volumes of %s from its image\n", s)
		// The copy runs before the process starts, so `true` exiting means the
		// volume is complete.
		_, err := c.Runner.Run(ctx, execx.Cmd{
			Name: "docker",
			Args: append(slices.Clone(base), "run", "--rm", "--no-deps", "--build", "-T", "--entrypoint", "true", s),
			Dir:  dir,
			Env:  env,
			Live: true,
		})
		if err != nil {
			// A half-seeded volume is never filled again: drop it so the next up
			// starts over; a killed wtm skips this, its pending files do it then.
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if _, rmErr := c.Runner.Run(cctx, execx.Cmd{Name: "docker", Args: append([]string{"volume", "rm"}, fresh...)}); rmErr == nil {
				clearPending(pendingDir, fresh)
			}
			return fmt.Errorf("seeding the shared volumes from %s: %w", s, err)
		}
	}
	clearPending(pendingDir, fresh)
	return nil
}

func clearPending(dir string, volumes []string) {
	for _, v := range volumes {
		_ = os.Remove(filepath.Join(dir, v))
	}
}

// lockSharedVolumes serialises creating and seeding shared volumes across every
// wtm on the machine: a stack waits until another's seeding is over.
func (c *Client) lockSharedVolumes(ctx context.Context) (func(), error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	l := flock.New(filepath.Join(dir, "volumes.lock"))
	locked, err := l.TryLock()
	if err == nil && !locked {
		fmt.Fprintln(c.Out, "waiting for another wtm to finish seeding a shared volume")
		locked, err = l.TryLockContext(ctx, 500*time.Millisecond)
	}
	if err != nil || !locked {
		return nil, fmt.Errorf("taking %s: %w", l.Path(), err)
	}
	return func() { _ = l.Unlock() }, nil
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
// env must match Up's WTM_DEPS_EXTERNAL, or a dependency volume reads as owned
// by this project and `--volumes` may remove it.
func (c *Client) Down(ctx context.Context, project, worktreeDir string, env []string) error {
	_, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "-p", project, "down", "--volumes"},
		Dir:  worktreeDir,
		Env:  env,
		Live: true,
	})
	return err
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
