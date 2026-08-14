package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Staleness says how far a dump has fallen behind the migrations it was built
// from. A stale dump still works, the application simply replays the delta on
// startup, so this is worth reporting and never worth blocking on.
type Staleness struct {
	Commits int  // commits touching migrations since the dump was taken
	Known   bool // false when the recorded revision cannot be compared
}

// Behind reports whether refreshing would save time.
func (s Staleness) Behind() bool { return s.Known && s.Commits > 0 }

// Describe renders the state for a listing.
func (s Staleness) Describe() string {
	switch {
	case !s.Known:
		return "unknown"
	case s.Commits == 0:
		return "up to date"
	case s.Commits == 1:
		return "1 commit behind"
	default:
		return fmt.Sprintf("%d commits behind", s.Commits)
	}
}

// Check compares the revision recorded next to the dump with the project's
// migration history.
func (m *Manager) Check(ctx context.Context, name string, p config.Project) Staleness {
	meta, err := m.ReadMeta(name)
	if err != nil || strings.TrimSpace(meta.GitRev) == "" {
		return Staleness{}
	}
	res, err := m.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", p.Dir, "log", "--oneline", meta.GitRev + "..HEAD", "--", p.BackupConfig().MigrationsPath},
	})
	if err != nil {
		// The revision may have been rebased away, in which case nothing can
		// be compared and saying so beats guessing.
		return Staleness{}
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return Staleness{Commits: count, Known: true}
}
