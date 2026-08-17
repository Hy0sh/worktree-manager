package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/config"
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
	var missing []string
	if !running[cfg.DBService] {
		missing = append(missing, cfg.DBService)
	}
	if len(missing) == 0 {
		m.logf("database of %s already running", name)
		return nil
	}
	if _, err := m.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: append([]string{"compose", "up", "-d"}, missing...),
		Dir:  p.Dir,
		Live: true,
	}); err != nil {
		return fmt.Errorf("starting stack %s: %w", name, err)
	}
	return nil
}

// waitFor retries a probe until it succeeds or the budget runs out.
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

func (m *Manager) psql(ctx context.Context, p config.Project, cfg config.Backup, sql string) (execx.Result, error) {
	return m.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "exec", "-T", cfg.DBService, "psql", "-U", cfg.DBUser, "-c", sql},
		Dir:  p.Dir,
	})
}

// tmpDBName keeps the identifier valid unquoted: my-app would not be.
func tmpDBName(project string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(project) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String() + "_snapshot_tmp"
}
