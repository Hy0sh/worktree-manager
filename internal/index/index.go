// Package index owns the branch→index mapping of a project's worktrees. It
// feeds the port formula and the compose project name, so an index must never
// change once a stack exists: allocated once, recorded, recovered from docker.
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

type Resolver struct {
	ConfigPath string
	Runner     execx.Runner
	Name       string // project name in the registry
	RepoName   string // filepath.Base of the project directory
	Out        io.Writer
	// Conflicts says why index n must not be handed out, or "" when it may.
	// The resolver knows nothing about ports; the worktree package does, and
	// with a stride of 1 services one port apart collide on neighbouring indices.
	Conflicts func(n int) string
}

func (r *Resolver) logf(format string, args ...any) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, format+"\n", args...)
	}
}

// conflicts is Conflicts with the nil check folded in.
func (r *Resolver) conflicts(n int) string {
	if r.Conflicts == nil {
		return ""
	}
	return r.Conflicts(n)
}

// Resolve tries, first answer winning: recorded → backfill from docker labels →
// git position if free and clean → allocation (MayAllocate only). pos is that
// position, kept so worktrees older than recorded indices keep their .env ports.
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
		// take hands the index out either way, but pins it in the registry only
		// when docker answered the survey: an allocation made blind is a guess,
		// not a fact worth recording forever.
		take := func(n int) error {
			idx = n
			if dockerOK {
				p.WorktreeIndices[branch] = n
				c.Projects[r.Name] = p
			}
			return nil
		}
		// A refused index is announced once: the fallback asks about pos, then
		// the allocation loop reaches pos again with the same answer.
		reported := map[int]bool{}
		skipped := func(n int, why string) {
			if !reported[n] {
				reported[n] = true
				r.logf("index %d skipped: %s", n, why)
			}
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
				return take(n)
			}
		}

		// Historical fallback: a worktree created --no-start before indices
		// were recorded has the git position's ports in its .env, so honour it
		// while it is free and no other branch's debris squats it.
		if pos > 0 && ownerOf(p.WorktreeIndices, pos) == "" &&
			!(dockerOK && hasLeftovers(labels, r.RepoName, pos, branch)) {
			// A new worktree's position is usually free, so this is the path a
			// plain create takes: it must ask the same question as the
			// allocation loop below.
			if why := r.conflicts(pos); why != "" {
				skipped(pos, why)
			} else {
				return take(pos)
			}
		}

		if mode != MayAllocate {
			return fmt.Errorf("%w for branch %q and no docker trace of its stack", ErrNoIndex, branch)
		}
		for n := 1; ; n++ {
			if ownerOf(p.WorktreeIndices, n) != "" {
				continue
			}
			if dockerOK && hasLeftovers(labels, r.RepoName, n, branch) {
				skipped(n, "docker still holds containers or volumes of a previous worktree there")
				continue
			}
			if why := r.conflicts(n); why != "" {
				skipped(n, why)
				continue
			}
			return take(n)
		}
	})
	return idx, err
}

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

func (r *Resolver) Recorded() map[string]int {
	cfg, err := config.Load(r.ConfigPath)
	if err != nil {
		return nil
	}
	return cfg.Projects[r.Name].WorktreeIndices
}

func ownerOf(indices map[string]int, n int) string {
	for branch, i := range indices {
		if i == n {
			return branch
		}
	}
	return ""
}
