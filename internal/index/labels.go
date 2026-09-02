package index

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// wtIndex spots the index a compose project label encodes.
var wtIndex = regexp.MustCompile(`-wt-(\d+)-`)

// MatchBranch finds the label belonging to this repo and branch among compose
// project labels. It extracts the candidate integer then rebuilds the
// canonical name, so stack.ProjectName stays the single naming authority.
func MatchBranch(labels []string, repoName, branch string) (int, string, bool) {
	for _, l := range labels {
		m := wtIndex.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 {
			continue
		}
		if stack.ProjectName(repoName, n, branch) == l {
			return n, l, true
		}
	}
	return 0, "", false
}

// hasLeftovers reports whether docker still holds resources of ANOTHER
// branch at this index. The branch's own resources are fine: reusing them is
// what backfill does anyway.
func hasLeftovers(labels []string, repoName string, n int, branch string) bool {
	prefix := stack.ProjectPrefix(repoName, n)
	own := stack.ProjectName(repoName, n, branch)
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) && l != own {
			return true
		}
	}
	return false
}

// dockerLabelsTimeout keeps a wedged daemon from hanging every command that
// resolves an index.
const dockerLabelsTimeout = 5 * time.Second

// dockerProjects lists every compose project docker still knows about, stopped
// containers and volumes included. ok is false only when `ps -a` itself fails:
// partial evidence can only understate leftovers, never overstate them.
func (r *Resolver) dockerProjects(ctx context.Context) (labels []string, ok bool) {
	// A missed deadline degrades to the registry, which is what this function
	// already does when docker refuses, so the bound costs nothing.
	ctx, cancel := context.WithTimeout(ctx, dockerLabelsTimeout)
	defer cancel()
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			labels = append(labels, name)
		}
	}
	res, err := r.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"ps", "-a", "--format", `{{.Label "com.docker.compose.project"}}`},
	})
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		add(strings.TrimSpace(line))
	}
	// Volumes do not support the .Label template, so the k=v list is parsed;
	// its key order is not stable, hence the per-entry scan.
	res, err = r.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"volume", "ls", "--format", "{{.Labels}}"},
	})
	if err != nil {
		// Missing volume labels can only make a dirty index look clean, never
		// the reverse, so what ps -a found is worth keeping rather than
		// discarding.
		r.logf("warning: docker volumes could not be listed, leftover detection may miss volume-only debris")
		return labels, true
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		for _, kv := range strings.Split(line, ",") {
			if v, found := strings.CutPrefix(strings.TrimSpace(kv), "com.docker.compose.project="); found {
				add(v)
			}
		}
	}
	return labels, true
}
