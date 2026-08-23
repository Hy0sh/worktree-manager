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

// ensureUp starts only the services that are down. Recreating a container that
// already runs is never harmless: the developer's stack goes through a full
// restart, dependency reinstall included, for no benefit here.
func (m *Manager) ensureUp(ctx context.Context, name string, p config.Project, cfg config.Backup) error {
	res, err := m.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "ps", "--services", "--status", "running"},
		Dir:  p.Dir,
	})
	if err != nil {
		return fmt.Errorf("state of stack %s: %w", name, err)
	}
	running := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			running[s] = true
		}
	}
	// Only the database has to run: migrations happen in their own container.
	if running[cfg.DBService] {
		m.logf("database of %s already running", name)
		return nil
	}
	if _, err := m.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "up", "-d", cfg.DBService},
		Dir:  p.Dir,
		Live: true,
	}); err != nil {
		return fmt.Errorf("starting stack %s: %w", name, err)
	}
	return nil
}

func (m *Manager) waitFor(ctx context.Context, label string, defaultAttempts int, probe execx.Cmd) error {
	attempts := defaultAttempts
	if m.MaxWaitAttempts > 0 {
		attempts = m.MaxWaitAttempts
	}
	interval := m.WaitInterval
	if interval <= 0 && m.MaxWaitAttempts == 0 {
		interval = defaultWaitInterval
	}
	var last error
	for i := 0; i < attempts; i++ {
		if _, err := m.Runner.Run(ctx, probe); err == nil {
			return nil
		} else {
			last = err
		}
		if i < attempts-1 && interval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
	}
	return fmt.Errorf("timed out waiting for %s (%d attempts): %w", label, attempts, last)
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
