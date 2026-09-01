package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/safefile"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// projectPorts reads the two inputs of every allocation: the ports the merged
// compose file publishes, and the stride the indices step by.
func projectPorts(o Options) ([]compose.ServicePort, int, error) {
	services, err := compose.MergedServicePorts(o.Project.Dir)
	if err != nil {
		return nil, 0, err
	}
	return services, stack.Stride(o.Project.Dir), nil
}

func allocations(o Options, wt stack.Worktree) ([]stack.Allocation, error) {
	services, stride, err := projectPorts(o)
	if err != nil {
		return nil, err
	}
	return stack.Allocate(services, wt.Index, stride, o.Project.PortOffset)
}

func allocatePorts(ctx context.Context, o Options, wt stack.Worktree, dest string) error {
	allocations, err := allocations(o, wt)
	if err != nil {
		return err
	}
	if len(allocations) == 0 {
		o.logf("warning: this project publishes no port, nothing to isolate")
		return nil
	}
	// Literal ports cannot be reached through the environment, so a generated
	// compose file rebases them. A versioned .env carries no environment at all,
	// and that file then has to restate every port or it isolates nothing.
	envTracked := tracked(ctx, o, dest, ".env")
	override := stack.PortsOverride(allocations, envTracked)
	path := filepath.Join(dest, portsOverride)
	if override == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := safefile.Write(dest, path, []byte(override), 0o644); err != nil {
		return fmt.Errorf("writing the ports compose file: %w", err)
	}
	// Writing into a tracked .env would dirty the worktree on every start, and
	// the generated compose file now carries every port docker needs.
	if envTracked {
		o.logf("note: .env is tracked by git, ports are only set in %s", portsOverride)
		return nil
	}
	return stack.WriteEnvOverrides(dest, allocations)
}

// portClash checks a candidate index against recorded worktrees only, since
// those are the ones that can run at the same time as the new one. The stride
// stays put: changing it would move every existing worktree's ports.
func portClash(o Options) func(n int) string {
	services, stride, err := projectPorts(o)
	if err != nil {
		return func(int) string { return "" }
	}
	recorded := o.Resolver.Recorded()
	return func(n int) string {
		mine, err := stack.Allocate(services, n, stride, o.Project.PortOffset)
		if err != nil {
			return ""
		}
		for branch, idx := range recorded {
			if branch == o.Branch || idx == n {
				continue
			}
			theirs, err := stack.Allocate(services, idx, stride, o.Project.PortOffset)
			if err != nil {
				continue
			}
			for _, a := range mine {
				for _, b := range theirs {
					if a.Port == b.Port {
						return fmt.Sprintf("%s would publish %d, which %s already publishes for %s "+
							"(raise portStride in .wtcrc.json to spread the indices further apart)",
							a.Service, a.Port, branch, b.Service)
					}
				}
			}
		}
		return ""
	}
}

// endpoints pairs each service with the port it actually listens on in this
// worktree, so the output is a list of addresses to open rather than the raw
// block of variables written into .env.
func endpoints(o Options, wt stack.Worktree) []string {
	allocations, err := allocations(o, wt)
	if err != nil || len(allocations) == 0 {
		return nil
	}

	// A service can publish several ports (mailhog exposes SMTP and a web UI),
	// and repeating its bare name would leave no way to tell them apart. The
	// variable name, or the container port, carries the distinction.
	count := map[string]int{}
	for _, a := range allocations {
		count[a.Service]++
	}

	type entry struct{ label, address string }
	var entries []entry
	width := 0
	for _, a := range allocations {
		label := a.Service
		if count[a.Service] > 1 {
			label += "/" + compose.PortLabel(compose.ServicePort{Service: a.Service, Var: a.Var, Container: a.Container})
		}
		address := "localhost:" + strconv.Itoa(a.Port)
		if (compose.ServicePort{Container: a.Container}).IsWeb() {
			address = "http://" + address
		}
		if len(label) > width {
			width = len(label)
		}
		entries = append(entries, entry{label, address})
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%-*s  %s", width, e.label, e.address))
	}
	return out
}
