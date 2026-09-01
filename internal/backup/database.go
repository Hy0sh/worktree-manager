package backup

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// ensureUp starts the database when it is down (recreating a running one
// restarts the developer's stack for nothing) and returns the cleanup that
// undoes only what wtm started; see cleanupStarted for which command that is.
func (m *Manager) ensureUp(ctx context.Context, name string, p config.Project, cfg config.Backup) (func(), error) {
	noop := func() {}
	running, err := m.services(ctx, p, "ps", "--services", "--status", "running")
	if err != nil {
		return noop, fmt.Errorf("state of stack %s: %w", name, err)
	}
	// Only the database has to run: migrations happen in their own container.
	if running[cfg.DBService] {
		m.logf("database of %s already running", name)
		return noop, nil
	}
	existing, err := m.services(ctx, p, "ps", "-a", "--services")
	if err != nil {
		return noop, fmt.Errorf("state of stack %s: %w", name, err)
	}
	if _, err := m.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "up", "-d", cfg.DBService},
		Dir:  p.Dir,
		Live: true,
	}); err != nil {
		// The failed up may have left a created container behind: same cleanup.
		m.cleanupStarted(ctx, p, cfg, existing)()
		return noop, fmt.Errorf("starting stack %s: %w", name, err)
	}
	return m.cleanupStarted(ctx, p, cfg, existing), nil
}

// cleanupStarted undoes only what wtm itself started. Nothing existed: down,
// volumes included, they are all wtm's. A stopped db existed: up reused it and
// the developer's data in it, so it goes back to stopped, nothing removed.
func (m *Manager) cleanupStarted(ctx context.Context, p config.Project, cfg config.Backup, existing map[string]bool) func() {
	return func() {
		var args []string
		switch {
		case len(existing) == 0:
			args = []string{"compose", "down", "--volumes"}
		case existing[cfg.DBService]:
			args = []string{"compose", "stop", cfg.DBService}
		default:
			args = []string{"compose", "rm", "-f", "-s", "-v", cfg.DBService}
		}
		if _, err := m.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: args, Dir: p.Dir}); err != nil {
			m.logf("warning: the database started for the refresh could not be taken down: %v", err)
		}
	}
}

// services lists what `compose ps` answers for the given selection.
func (m *Manager) services(ctx context.Context, p config.Project, args ...string) (map[string]bool, error) {
	res, err := m.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: append([]string{"compose"}, args...), Dir: p.Dir})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out[s] = true
		}
	}
	return out, nil
}

func (m *Manager) waitFor(ctx context.Context, label string, defaultAttempts int, probe execx.Cmd) error {
	attempts := defaultAttempts
	if m.MaxWaitAttempts > 0 {
		attempts = m.MaxWaitAttempts
	}
	// MaxWaitAttempts is the tests' hook, and they want no sleep at all.
	var interval time.Duration
	if m.MaxWaitAttempts == 0 {
		interval = defaultWaitInterval
	}
	return execx.WaitFor(ctx, m.Runner, label, attempts, interval, probe)
}

// assertPopulated refuses to dump a throwaway database no migration reached.
// {{database}} only works if the app honours the variable it is mapped to; when
// it does not, the migrations hit the project's own database and this dump would
// bring every worktree up empty. Only a count that reads as zero fails: an
// unreadable probe is a diagnosis wtm could not make, not a verdict.
func (m *Manager) assertPopulated(ctx context.Context, p config.Project, cfg config.Backup, eng dbengine.Engine, db string) error {
	res, err := m.execInDB(ctx, p, cfg, eng.ObjectCountArgs(cfg.DBUser, db))
	if err != nil {
		m.logf("warning: could not count what %s holds, dumping it as is: %v", db, err)
		return nil
	}
	count, convErr := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if convErr != nil {
		m.logf("warning: could not read how much %s holds, dumping it as is", db)
		return nil
	}
	if count > 0 {
		return nil
	}
	return fmt.Errorf("the migrations left %s empty, so its dump would bring every worktree up on an empty database.\n"+
		"`%s` ran, but against another database than the throwaway one: map the variable the app reads to %s in the project's `backup.env`, "+
		"as in --env DATABASE_URL='postgresql://user:pass@db:5432/%s'",
		db, cfg.MigrateCommand, config.DatabasePlaceholder, config.DatabasePlaceholder)
}

func (m *Manager) execInDB(ctx context.Context, p config.Project, cfg config.Backup, args []string) (execx.Result, error) {
	return m.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append([]string{"compose", "exec", "-T", cfg.DBService}, args...),
		Dir:  p.Dir,
	})
}
