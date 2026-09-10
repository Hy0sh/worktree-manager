package worktree

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

type Entry struct {
	stack.Worktree
	// Status is "up", "down", "adoptable" for a worktree wtm has not adopted,
	// or "-" when docker could not be reached.
	Status string
}

// StatusUnknown is shown when docker did not answer in time. A listing is a
// read-only question about git and must never hang on an unresponsive daemon.
const StatusUnknown = "-"

// StatusAdoptable is shown for a worktree git lists and wtm has not adopted.
// It stands where up and down would: such a worktree has no index, hence no
// stack of its own to be up or down.
const StatusAdoptable = "adoptable"

// dockerStatusTimeout keeps the listing responsive whatever docker is doing.
const dockerStatusTimeout = 5 * time.Second

// Adoptable says the entry is there to be seen and named, not to be addressed:
// with no index, `adopt` is the only verb that has anything to say to it.
func (e Entry) Adoptable() bool { return e.Status == StatusAdoptable }

// List answers about every linked worktree, adopted or not: a worktree wtm has
// not adopted is precisely the one somebody has to name to adopt it, and it
// used to be the one the listing left out.
func List(ctx context.Context, o Options) ([]Entry, error) {
	worktrees, err := o.Stack.All(ctx)
	if err != nil {
		return nil, err
	}
	// A project with no compose file has no stack, so it is neither up nor down.
	running := map[string]bool(nil)
	if hasCompose(o.Project.Dir) {
		running = runningProjects(ctx, o.Runner)
	}
	indices := o.Resolver.Recorded()
	runningLabels := make([]string, 0, len(running))
	for name := range running {
		runningLabels = append(runningLabels, name)
	}
	entries := make([]Entry, 0, len(worktrees))
	for _, wt := range worktrees {
		if !wt.UnderRoot && !o.Stack.Managed[wt.Branch] {
			entries = append(entries, Entry{Worktree: wt, Status: StatusAdoptable})
			continue
		}
		wt.Index = indices[wt.Branch]
		status := StatusUnknown
		if running != nil {
			status = "down"
			if wt.Index > 0 && running[o.projectName(wt)] {
				status = "up"
			} else if wt.Index == 0 {
				// Not recorded yet: an old stack may still run under the
				// index docker gave it; match by branch instead of by name.
				if _, _, ok := index.MatchBranch(runningLabels, filepath.Base(o.Project.Dir), wt.Branch); ok {
					status = "up"
				}
			}
		}
		entries = append(entries, Entry{Worktree: wt, Status: status})
	}
	return entries, nil
}

// runningProjects returns the compose projects with a running container, or
// nil when docker cannot be reached.
func runningProjects(ctx context.Context, runner execx.Runner) map[string]bool {
	ctx, cancel := context.WithTimeout(ctx, dockerStatusTimeout)
	defer cancel()
	res, err := runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"ps", "--format", `{{.Label "com.docker.compose.project"}}`},
	})
	if err != nil {
		return nil
	}
	running := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			running[name] = true
		}
	}
	return running
}
