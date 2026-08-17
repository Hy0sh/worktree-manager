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

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
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
						"stop the old stack with `docker compose -p %s down -v`, then `wtm start %s` will allocate a fresh index",
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
			if !dockerOK {
				// A blind guess made without docker evidence must not become
				// permanent: a neighbour that hasn't been backfilled yet may
				// really be running at this index. Resolve for this call
				// only, so a later Resolve (once docker answers again) is
				// still free to backfill or allocate for real.
				idx = pos
				return nil
			}
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
			if !dockerOK {
				// Same reasoning as the fallback above: an allocation made
				// blind is a guess, not a fact worth pinning forever.
				idx = n
				return nil
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

// ownerOf returns which branch holds an index, "" when free.
func ownerOf(indices map[string]int, n int) string {
	for branch, i := range indices {
		if i == n {
			return branch
		}
	}
	return ""
}
