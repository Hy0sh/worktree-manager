// Package dockermem measures what the Docker VM actually has and uses.
//
// Declared limits are useless for this: one my-app stack declares more
// than 13 GB of mem_limit across its services yet runs in about 2 GB, because
// mem_limit is a ceiling and not a reservation. Only measurement tells you
// whether one more stack fits.
package dockermem

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// WarnRatio is the share of the Docker VM above which starting one more stack
// is worth a warning.
const WarnRatio = 0.85

// Usage is a snapshot of the Docker VM.
type Usage struct {
	Total int64 // bytes available to the VM
	Used  int64 // bytes used by every running container
	// StackUsed excludes one-off `compose run` containers: a migration
	// container peaking at 6 GB is not what a stack costs, and counting it
	// would triple the estimate.
	StackUsed int64
	Projects  int // distinct compose projects, one-offs excluded
}

// PerProject is the average cost of a running stack, the best available
// estimate of what one more would cost.
func (u Usage) PerProject() int64 {
	if u.Projects <= 0 {
		return 0
	}
	return u.StackUsed / int64(u.Projects)
}

// Projected is what usage would look like with one more stack.
func (u Usage) Projected() int64 { return u.Used + u.PerProject() }

// Tight reports whether starting one more stack would cross WarnRatio.
func (u Usage) Tight() bool {
	if u.Total <= 0 || u.Projects <= 0 {
		return false
	}
	return float64(u.Projected()) > float64(u.Total)*WarnRatio
}

// Warning is the message to show, empty when there is nothing to say.
func (u Usage) Warning() string {
	if !u.Tight() {
		return ""
	}
	return fmt.Sprintf("attention: %d stack(s) occupent déjà %s sur les %s de la VM Docker ; "+
		"une de plus (~%s estimés) porterait le total à ~%s. Arrête une stack (`wtm stop <branche>`) "+
		"ou augmente la RAM de Docker Desktop si ça coince.",
		u.Projects, Human(u.Used), Human(u.Total), Human(u.PerProject()), Human(u.Projected()))
}

// Read samples the VM: total memory, per-container usage, and how many compose
// projects are running.
func Read(ctx context.Context, runner execx.Runner) (Usage, error) {
	var u Usage

	res, err := runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"info", "--format", "{{.MemTotal}}"}})
	if err != nil {
		return u, fmt.Errorf("mémoire totale de la VM Docker: %w", err)
	}
	if u.Total, err = strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64); err != nil {
		return u, fmt.Errorf("mémoire totale illisible (%q): %w", strings.TrimSpace(res.Stdout), err)
	}

	// Which containers belong to a stack, and which are throwaway `run` ones.
	res, err = runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"ps", "--format", `{{.Names}}|{{.Label "com.docker.compose.project"}}|{{.Label "com.docker.compose.oneoff"}}`},
	})
	if err != nil {
		return u, fmt.Errorf("projets compose en cours: %w", err)
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
		return u, fmt.Errorf("consommation des conteneurs: %w", err)
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
	return u, nil
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
	return 0, fmt.Errorf("taille non reconnue: %q", s)
}

// Human renders bytes the way the messages read best.
func Human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f Go", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f Mo", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d o", n)
	}
}
