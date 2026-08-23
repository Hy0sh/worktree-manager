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
	// Status is "up", "down", or "-" when docker could not be reached.
	Status string
}

// StatusUnknown is shown when docker did not answer in time. A listing is a
// read-only question about git and must never hang on an unresponsive daemon.
const StatusUnknown = "-"

// dockerStatusTimeout keeps the listing responsive whatever docker is doing.
const dockerStatusTimeout = 5 * time.Second

func List(ctx context.Context, o Options) ([]Entry, error) {
	worktrees, err := o.Stack.Worktrees(ctx)
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
