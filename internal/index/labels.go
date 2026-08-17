package index

import (
	"context"
	"regexp"
	"strconv"
	"strings"

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

// dockerProjects lists every compose project docker still knows about,
// stopped containers and volumes included. ok is false only when the first
// probe (ps -a) fails, meaning docker itself is unreachable and resolution
// must degrade to the registry alone; if only the volume probe fails, the
// container labels already gathered are still returned with ok == true,
// since partial evidence can only understate leftovers, never overstate them.
func (r *Resolver) dockerProjects(ctx context.Context) (labels []string, ok bool) {
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
		// The container probe already answered: that partial evidence is
		// still worth keeping. Missing volume labels can only make a dirty
		// index look clean (a false negative that skips leftover detection
		// for volume-only debris), never the reverse, so it is safe to
		// proceed with what ps -a found rather than discard it.
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
