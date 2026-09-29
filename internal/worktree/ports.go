package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/proxy"
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

// portEnv hands compose the port variables of this worktree, which it prefers
// to the .env when interpolating: a versioned .env carries none of them, and
// `environment: API_URL: http://localhost:${API_PORT}` then read the main port.
func portEnv(o Options, wt stack.Worktree) []string {
	allocations, err := allocations(o, wt)
	if err != nil {
		return nil
	}
	var env []string
	for _, a := range allocations {
		if a.Var != "" {
			env = append(env, a.Var+"="+strconv.Itoa(a.Port))
		}
	}
	return env
}

// portClash checks a candidate index against recorded worktrees only, since
// those are the ones that can run at the same time as the new one, in every
// project: offsets step by 1000, less than the spread of the ports they shift.
// The stride and offsets stay put: changing either moves existing ports.
func portClash(o Options) func(n int) string {
	services, stride, err := projectPorts(o)
	if err != nil {
		return func(int) string { return "" }
	}
	cfg, err := config.Load(o.Resolver.ConfigPath)
	if err != nil {
		return func(int) string { return "" }
	}
	type neighbour struct {
		who, remedy string
		sameProject bool
		index       int
		ports       []stack.Allocation
	}
	var neighbours []neighbour
	// Sorted twice over: map order would name a different neighbour each run,
	// for what is one and the same clash.
	for _, name := range cfg.Names() {
		p := cfg.Projects[name]
		theirServices, theirStride := services, stride
		prefix, remedy := "", "raise portStride in .wtcrc.json to spread the indices further apart"
		if name != o.Name {
			if theirServices, err = compose.MergedServicePorts(p.Dir); err != nil {
				continue
			}
			theirStride = stack.Stride(p.Dir)
			prefix, remedy = name+"/", "raise port_offset for one of the two projects in config.json"
		}
		for _, branch := range slices.Sorted(maps.Keys(p.WorktreeIndices)) {
			if name == o.Name && branch == o.Branch {
				continue
			}
			idx := p.WorktreeIndices[branch]
			theirs, err := stack.Allocate(theirServices, idx, theirStride, p.PortOffset)
			if err != nil {
				continue
			}
			neighbours = append(neighbours, neighbour{prefix + branch, remedy, name == o.Name, idx, theirs})
		}
	}
	return func(n int) string {
		mine, err := stack.Allocate(services, n, stride, o.Project.PortOffset)
		if err != nil {
			return ""
		}
		for _, nb := range neighbours {
			if nb.sameProject && nb.index == n {
				continue
			}
			for _, a := range mine {
				for _, b := range nb.ports {
					if a.Port == b.Port {
						return fmt.Sprintf("%s would publish %d, which %s already publishes for %s (%s)",
							a.Service, a.Port, nb.who, b.Service, nb.remedy)
					}
				}
			}
		}
		return ""
	}
}

// startedServices names what the profile brought up, depends_on included, nil
// for everything: without a profile, or when the compose files cannot be read.
func startedServices(o Options) []string {
	named, err := o.Project.ServicesFor(o.Profile)
	if err != nil || named == nil {
		return nil
	}
	started, err := compose.WithDependencies(o.Project.Dir, named)
	if err != nil {
		return nil
	}
	return started
}

// startedBy keeps the ports of what the profile brought up: listing a service
// left down hands out an address nothing answers.
func startedBy(o Options, allocations []stack.Allocation) []stack.Allocation {
	started := startedServices(o)
	if started == nil {
		return allocations
	}
	var kept []stack.Allocation
	for _, a := range allocations {
		if slices.Contains(started, a.Service) {
			kept = append(kept, a)
		}
	}
	return kept
}

// endpoints pairs each service with the port it actually listens on in this
// worktree, so the output is a list of addresses to open rather than the raw
// block of variables written into .env.
func endpoints(ctx context.Context, o Options, wt stack.Worktree) []string {
	allocations, err := allocations(o, wt)
	if err != nil {
		return nil
	}
	allocations = startedBy(o, allocations)

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
	urls, via := routedURLs(ctx, o, wt)
	var urlEntries []entry
	for _, service := range slices.Sorted(maps.Keys(urls)) {
		for _, address := range urls[service] {
			// The service's own port line may carry the bare name already.
			label := service + "/url"
			width = max(width, len(label))
			urlEntries = append(urlEntries, entry{label, address})
		}
	}
	format := func(indent string, es []entry) []string {
		out := make([]string, 0, len(es))
		for _, e := range es {
			out = append(out, fmt.Sprintf("%s%-*s  %s", indent, width, e.label, e.address))
		}
		return out
	}
	if len(urlEntries) == 0 {
		return format("", entries)
	}
	// What people open comes first. The titles and the blank line keep
	// `awk '$1 == "api/url"'` working, since no service is named after them.
	title := "urls"
	if via != "" {
		title += ", through " + via
	}
	out := append([]string{title}, format("  ", urlEntries)...)
	if len(entries) > 0 {
		out = append(append(out, "", "ports"), format("  ", entries)...)
	}
	return out
}

// composeConfigTimeout bounds the one docker call printing the addresses
// costs, which must never hang a start that already succeeded.
const composeConfigTimeout = 10 * time.Second

// routedURLs reads what a known proxy routes to each started service, from
// compose's own rendering: host names resolve ${VAR} against the worktree's
// .env, and ports are the worktree's. via names the proxy, "" for none.
func routedURLs(ctx context.Context, o Options, wt stack.Worktree) (urls map[string][]string, via string) {
	if !mentionsProxy(o, wt) {
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(ctx, composeConfigTimeout)
	defer cancel()
	res, err := o.Runner.Run(ctx, o.composeCmd(wt, "config", "--format", "json"))
	if err != nil {
		return nil, ""
	}
	var cfg struct {
		Services map[string]proxy.Service `json:"services"`
	}
	if json.Unmarshal([]byte(res.Stdout), &cfg) != nil {
		return nil, ""
	}
	// A proxy the profile left down routes nothing, and neither does a
	// service left down behind a running one.
	if started := startedServices(o); started != nil {
		maps.DeleteFunc(cfg.Services, func(name string, _ proxy.Service) bool { return !slices.Contains(started, name) })
	}
	return proxy.URLs(cfg.Services)
}

// mentionsProxy spares the docker call to every project that runs no proxy
// wtm knows, which is most of them.
func mentionsProxy(o Options, wt stack.Worktree) bool {
	files, err := composeFiles(o, wt.Path)
	if err != nil {
		return false
	}
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil && proxy.Mentioned(string(data)) {
			return true
		}
	}
	return false
}
