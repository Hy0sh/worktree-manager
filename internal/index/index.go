// Package index owns the branch→index mapping of a project's worktrees. The
// index feeds the port formula and the compose project name, so it must
// never change once a stack exists; this package allocates it once, records
// it in the registry, and recovers it from docker for worktrees that predate
// the recording.
package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// Mode says whether a resolution may invent a new index. Commands that bring
// a stack up may; commands that address an existing stack (stop, remove,
// exec) must not, or they would "find" a stack that never existed.
type Mode int

const (
	MustExist Mode = iota
	MayAllocate
)

// ErrNoIndex means the branch has no recorded index and no docker trace:
// as far as stacks are concerned, this worktree never started one.
var ErrNoIndex = errors.New("no recorded index")

// Resolver answers "which index does this branch own" for one project.
type Resolver struct {
	ConfigPath string
	Runner     execx.Runner
	Name       string // project name in the registry
	RepoName   string // filepath.Base of the project directory
	Out        io.Writer
}

func (r *Resolver) logf(format string, args ...any) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, format+"\n", args...)
	}
}

// Resolve returns the branch's index. pos is the 1-based position git lists
// the worktree at (0 when unknown), used only as the historical fallback so
// worktrees created before indices were recorded keep the ports their .env
// already carries. Steps, first answer wins: recorded → backfill from docker
// labels → git position if free and clean → allocation (MayAllocate only).
func (r *Resolver) Resolve(ctx context.Context, branch string, pos int, mode Mode) (int, error) {
	// Nominal path: recorded, read-only, no lock and no docker.
	cfg, err := config.Load(r.ConfigPath)
	if err != nil {
		return 0, err
	}
	if n := cfg.Projects[r.Name].WorktreeIndices[branch]; n > 0 {
		return n, nil
	}

	// Docker is surveyed before taking the lock: the critical section must
	// stay a read-modify-write of a small file, never an external command.
	labels, dockerOK := r.dockerProjects(ctx)
	if !dockerOK {
		r.logf("warning: docker did not answer, resolving the index from the registry alone")
	}

	var idx int
	err = config.WithLock(r.ConfigPath, func(c *config.Config) error {
		p, ok := c.Projects[r.Name]
		if !ok {
			return fmt.Errorf("project %q is not registered", r.Name)
		}
		if p.WorktreeIndices == nil {
			p.WorktreeIndices = map[string]int{}
		}
		// Re-check under the lock: another wtm may have recorded meanwhile.
		if n := p.WorktreeIndices[branch]; n > 0 {
			idx = n
			return nil
		}
		record := func(n int) {
			p.WorktreeIndices[branch] = n
			c.Projects[r.Name] = p
			idx = n
		}

		// Backfill: the stack's own containers or volumes carry the index in
		// their compose project label. What docker knows is the truth the
		// registry is missing.
		if dockerOK {
			if n, label, found := MatchBranch(labels, r.RepoName, branch); found {
				if owner := ownerOf(p.WorktreeIndices, n); owner != "" && owner != branch {
					return fmt.Errorf("branch %q left a stack at index %d, which now belongs to branch %q; "+
						"stop the old stack with `docker compose -p %s down`, then `wtm start %s` will allocate a fresh index",
						branch, n, owner, label, branch)
				}
				record(n)
				return nil
			}
		}

		// Historical fallback: before indices were recorded, the index was
		// the git listing position. A worktree created --no-start back then
		// has that position's ports in its .env, so honour it while it is
		// still free and no other branch's debris squats it.
		if pos > 0 && ownerOf(p.WorktreeIndices, pos) == "" &&
			!(dockerOK && hasLeftovers(labels, r.RepoName, pos, branch)) {
			record(pos)
			return nil
		}

		if mode != MayAllocate {
			return fmt.Errorf("%w for branch %q and no docker trace of its stack", ErrNoIndex, branch)
		}
		for n := 1; ; n++ {
			if ownerOf(p.WorktreeIndices, n) != "" {
				continue
			}
			if dockerOK && hasLeftovers(labels, r.RepoName, n, branch) {
				r.logf("index %d skipped: docker still holds containers or volumes of a previous worktree there", n)
				continue
			}
			record(n)
			return nil
		}
	})
	return idx, err
}

// Release forgets the branch's index once its worktree is removed.
func (r *Resolver) Release(branch string) error {
	return config.WithLock(r.ConfigPath, func(c *config.Config) error {
		p, ok := c.Projects[r.Name]
		if !ok {
			return nil
		}
		delete(p.WorktreeIndices, branch)
		c.Projects[r.Name] = p
		return nil
	})
}

// Recorded returns the saved branch→index map, for read-only display.
func (r *Resolver) Recorded() map[string]int {
	cfg, err := config.Load(r.ConfigPath)
	if err != nil {
		return nil
	}
	return cfg.Projects[r.Name].WorktreeIndices
}

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

// ownerOf returns which branch holds an index, "" when free.
func ownerOf(indices map[string]int, n int) string {
	for branch, i := range indices {
		if i == n {
			return branch
		}
	}
	return ""
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
// stopped containers and volumes included. ok is false when docker cannot be
// reached, in which case resolution degrades to the registry alone.
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
		return labels, false
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
