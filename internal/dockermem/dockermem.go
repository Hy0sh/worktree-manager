// Package dockermem measures the memory a stack has to fit into, and what is
// already in it. mem_limit is a ceiling and not a reservation, so a stack
// declaring 13 GB across its services can run in 2 GB. See Usage.Shared.
package dockermem

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// WarnRatio is the share of the available memory above which starting one more
// stack is worth a warning.
const WarnRatio = 0.85

type Usage struct {
	Total int64 // bytes the containers have to fit into
	Used  int64 // bytes already used, by the containers alone unless Shared
	// StackUsed excludes one-off `compose run` containers: a migration
	// container peaking at 6 GB is not what a stack costs, and counting it
	// would triple the estimate.
	StackUsed int64
	Projects  int // distinct compose projects, one-offs excluded
	// Shared says the daemon runs on the very machine wtm does, a native Linux
	// docker: Used is then what the whole machine uses, since a desktop holding
	// 6 GB is pressure the containers compete with but cannot see.
	Shared bool
}

// PerProject is the average cost of a running stack, the best available
// estimate of what one more would cost.
func (u Usage) PerProject() int64 {
	if u.Projects <= 0 {
		return 0
	}
	return u.StackUsed / int64(u.Projects)
}

func (u Usage) Projected() int64 { return u.Used + u.PerProject() }

func (u Usage) Tight() bool {
	if u.Total <= 0 || u.Projects <= 0 {
		return false
	}
	return float64(u.Projected()) > float64(u.Total)*WarnRatio
}

func (u Usage) Warning() string {
	if !u.Tight() {
		return ""
	}
	if u.Shared {
		// Naming what the stacks account for keeps the number actionable: the
		// rest of the pressure is the user's own session, and no `wtm stop`
		// will free it.
		return fmt.Sprintf("warning: this machine uses %s of its %s, %d stack(s) accounting for %s; "+
			"one more (~%s estimated) would bring the total to ~%s. Stop a stack "+
			"(`wtm stop <branch>`, or `wtm stop --all`), or free memory elsewhere.",
			Human(u.Used), Human(u.Total), u.Projects, Human(u.StackUsed),
			Human(u.PerProject()), Human(u.Projected()))
	}
	return fmt.Sprintf("warning: %d stack(s) already use %s out of the %s of the Docker VM; "+
		"one more (~%s estimated) would bring the total to ~%s. Stop a stack (`wtm stop <branch>`) "+
		"or raise the Docker VM's memory limit if it gets tight.",
		u.Projects, Human(u.Used), Human(u.Total), Human(u.PerProject()), Human(u.Projected()))
}

// procMemInfo is where a Linux kernel publishes the machine's own memory.
const procMemInfo = "/proc/meminfo"

func Read(ctx context.Context, runner execx.Runner) (Usage, error) {
	return read(ctx, runner, procMemInfo)
}

// read takes the path of the local kernel's meminfo so the suite never depends
// on the machine it runs on; empty skips the check.
func read(ctx context.Context, runner execx.Runner, meminfo string) (Usage, error) {
	var u Usage

	res, err := runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"info", "--format", "{{.MemTotal}}"}})
	if err != nil {
		return u, fmt.Errorf("total memory of the Docker VM: %w", err)
	}
	if u.Total, err = strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64); err != nil {
		return u, fmt.Errorf("unreadable total memory (%q): %w", strings.TrimSpace(res.Stdout), err)
	}

	// Which containers belong to a stack, and which are throwaway `run` ones.
	res, err = runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"ps", "--format", `{{.Names}}|{{.Label "com.docker.compose.project"}}|{{.Label "com.docker.compose.oneoff"}}`},
	})
	if err != nil {
		return u, fmt.Errorf("running compose projects: %w", err)
	}
	project := map[string]string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) < 3 || parts[0] == "" || parts[1] == "" {
			continue
		}
		if strings.EqualFold(parts[2], "True") {
			continue // one-off: real memory, but not representative of a stack
		}
		project[parts[0]] = parts[1]
		seen[parts[1]] = true
	}
	u.Projects = len(seen)

	res, err = runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"stats", "--no-stream", "--format", "{{.Name}}|{{.MemUsage}}"},
	})
	if err != nil {
		return u, fmt.Errorf("container consumption: %w", err)
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		name, usage, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok {
			continue
		}
		used, _, ok := strings.Cut(usage, "/")
		if !ok {
			continue
		}
		n, err := ParseSize(strings.TrimSpace(used))
		if err != nil {
			continue
		}
		u.Used += n
		if _, inStack := project[name]; inStack {
			u.StackUsed += n
		}
	}
	// One kernel for both: the daemon reports the same total the local
	// /proc/meminfo does. A VM (Docker Desktop) or a remote daemon reports
	// another budget, where the containers' own sum stays the right measure.
	if used, total, ok := hostMemory(meminfo); ok && sameMachine(u.Total, total) {
		u.Used = used
		u.Shared = true
	}
	return u, nil
}

// sameMachine allows a little slack: the two numbers come from the same kernel
// but travel through docker's own formatting.
func sameMachine(reported, local int64) bool {
	if reported <= 0 || local <= 0 {
		return false
	}
	diff := reported - local
	if diff < 0 {
		diff = -diff
	}
	return float64(diff) < float64(local)*0.01
}

// hostMemory reads what the machine uses and what it has. MemAvailable is the
// kernel's estimate of what a new workload could claim without swapping;
// MemFree would ignore the caches the kernel gives back under pressure.
func hostMemory(path string) (used, total int64, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	var available int64
	for _, line := range strings.Split(string(data), "\n") {
		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		// Every size in this file is printed in kB, whatever the field.
		n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if key == "MemTotal" {
			total = n * 1024
		} else {
			available = n * 1024
		}
	}
	if total <= 0 || available <= 0 || available > total {
		return 0, 0, false
	}
	return total - available, total, true
}

var units = []struct {
	suffix string
	factor float64
}{
	{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	{"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
}

// ParseSize reads the sizes docker stats prints, e.g. "334.2MiB", "1.47GiB".
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
			if err != nil {
				return 0, err
			}
			return int64(n * u.factor), nil
		}
	}
	return 0, fmt.Errorf("unrecognized size: %q", s)
}

func Human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
