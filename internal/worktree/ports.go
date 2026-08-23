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

func allocations(o Options, wt stack.Worktree) ([]stack.Allocation, error) {
	services, err := compose.MergedServicePorts(o.Project.Dir)
	if err != nil {
		return nil, err
	}
	return stack.Allocate(services, wt.Index, stack.Stride(o.Project.Dir), o.Project.PortOffset)
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
	// Ports written as literals cannot be reached through the environment, so
	// they are rebased in a generated compose file instead.
	override := stack.PortsOverride(allocations)
	path := filepath.Join(dest, portsOverride)
	if override == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := safefile.Write(dest, path, []byte(override), 0o644); err != nil {
		return fmt.Errorf("writing the ports compose file: %w", err)
	}
	// Writing into a tracked .env would dirty the worktree on every start, and
	// the generated compose file already carries the ports docker needs.
	if tracked(ctx, o, dest, ".env") {
		o.logf("note: .env is tracked by git, ports are only set in %s", portsOverride)
		return nil
	}
	return stack.WriteEnvOverrides(dest, allocations)
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
