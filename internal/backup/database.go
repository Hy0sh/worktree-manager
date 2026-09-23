package backup

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// ensureUp starts the database when it is down (recreating a running one
// restarts the developer's stack for nothing) and returns the cleanup that
// undoes only what wtm started; see cleanupStarted for which commands those are.
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
		m.cleanupStarted(ctx, p, cfg, existing, running)()
		return noop, fmt.Errorf("starting stack %s: %w", name, err)
	}
	return m.cleanupStarted(ctx, p, cfg, existing, running), nil
}

// cleanupStarted undoes only what wtm itself started. A stack a developer had
// merely downed keeps its data in named volumes, so even when no container was
// there only the containers wtm brought up, their anonymous volumes and the
// network are wtm's.
func (m *Manager) cleanupStarted(ctx context.Context, p config.Project, cfg config.Backup, existing, wasRunning map[string]bool) func() {
	return func() {
		var cmds [][]string
		for _, service := range m.startedServices(ctx, p, cfg, wasRunning) {
			// A service that already had a container keeps it: the developer
			// downed that stack, they did not delete it.
			if existing[service] {
				cmds = append(cmds, []string{"compose", "stop", service})
			} else {
				cmds = append(cmds, []string{"compose", "rm", "-f", "-s", "-v", service})
			}
		}
		if len(existing) == 0 {
			// Nothing of this stack was there, so the network is wtm's too.
			cmds = append(cmds, []string{"compose", "down"})
		}
		for _, args := range cmds {
			if _, err := m.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: args, Dir: p.Dir}); err != nil {
				m.logf("warning: what the refresh started could not be taken down: %v", err)
			}
		}
	}
}

// startedServices names what the refresh must take back down: the database, and
// with start_dependencies whatever runs now and was not *running* before, since
// `compose run` turns stopped containers back on. A parallel start gets swept too.
func (m *Manager) startedServices(ctx context.Context, p config.Project, cfg config.Backup, wasRunning map[string]bool) []string {
	started := []string{cfg.DBService}
	if !cfg.StartDependencies {
		return started
	}
	running, err := m.services(ctx, p, "ps", "--services", "--status", "running")
	if err != nil {
		m.logf("warning: the services started for the refresh cannot be listed, only the database is taken down: %v", err)
		return started
	}
	var deps []string
	for service := range running {
		if service != cfg.DBService && !wasRunning[service] {
			deps = append(deps, service)
		}
	}
	sort.Strings(deps) // a map iterates in no order, and the log has to be stable
	return append(started, deps...)
}

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

// assertPopulated refuses to dump a throwaway database no migration reached:
// an app ignoring the variable {{database}} is mapped to migrates its own
// database instead. An unreadable count is no verdict, only a zero one is.
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
