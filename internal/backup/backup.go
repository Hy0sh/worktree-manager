// Package backup produces and manages the pre-migrated Postgres dump shared by
// every worktree of a project.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

const (
	dbWaitAttempts      = 60 // postgres answers quickly once the container is up
	defaultWaitInterval = time.Second
)

// Meta travels next to the dump so `backup list` can say where it comes from.
type Meta struct {
	GeneratedAt time.Time `json:"generated_at"`
	GeneratedBy string    `json:"generated_by"`
	GitRev      string    `json:"git_rev"`
}

// Info is one line of `backup list`.
type Info struct {
	Name    string
	Present bool
	Size    int64
	Meta    Meta
}

// Manager owns the central backups directory.
type Manager struct {
	Runner          execx.Runner
	Root            string
	Out             io.Writer
	MaxWaitAttempts int
	WaitInterval    time.Duration
}

func (m *Manager) DumpPath(name string) string {
	return filepath.Join(m.Root, name, name+".dump")
}

func (m *Manager) MetaPath(name string) string {
	return m.DumpPath(name) + ".meta"
}

func (m *Manager) logf(format string, args ...any) {
	if m.Out != nil {
		fmt.Fprintf(m.Out, format+"\n", args...)
	}
}

// Refresh regenerates the dump: it starts db and backend if needed, migrates a
// throwaway database and dumps its schema. No seed data ever lands in there.
func (m *Manager) Refresh(ctx context.Context, name string, p config.Project) error {
	cfg := p.BackupConfig()
	if err := cfg.Validate(); err != nil {
		return err
	}
	db := tmpDBName(name)

	if err := m.ensureUp(ctx, name, p, cfg); err != nil {
		return err
	}
	if err := m.waitFor(ctx, "the database", dbWaitAttempts, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "exec", "-T", cfg.DBService, "pg_isready", "-U", cfg.DBUser},
		Dir:  p.Dir,
	}); err != nil {
		return err
	}
	// From here the throwaway database may exist, so always try to drop it.
	defer func() {
		_, _ = m.psql(ctx, p, cfg, "DROP DATABASE IF EXISTS "+db+";")
	}()

	if _, err := m.psql(ctx, p, cfg, "DROP DATABASE IF EXISTS "+db+";"); err != nil {
		return fmt.Errorf("cleaning up the temporary database: %w", err)
	}
	if _, err := m.psql(ctx, p, cfg, "CREATE DATABASE "+db+";"); err != nil {
		return fmt.Errorf("creating the temporary database: %w", err)
	}
	if err := m.migrate(ctx, p, cfg, db); err != nil {
		return err
	}

	if err := m.dump(ctx, name, p, cfg, db); err != nil {
		return err
	}
	return m.writeMeta(ctx, name, p)
}

// dump streams pg_dump to a temporary file and renames it only on success, so a
// failed refresh never leaves a partial dump behind.
func (m *Manager) dump(ctx context.Context, name string, p config.Project, cfg config.Backup, db string) error {
	final := m.DumpPath(name)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmpPath := final + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating the temporary dump: %w", err)
	}
	_, runErr := m.Runner.Run(ctx, execx.Cmd{
		Name:   "docker",
		Args:   []string{"compose", "exec", "-T", cfg.DBService, "pg_dump", "-U", cfg.DBUser, "-Fc", "--no-owner", "--no-privileges", "-d", db},
		Dir:    p.Dir,
		Stdout: tmp,
	})
	closeErr := tmp.Close()
	if runErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("pg_dump: %w", runErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publishing the dump: %w", err)
	}
	m.logf("dump written: %s", final)
	return nil
}

func (m *Manager) writeMeta(ctx context.Context, name string, p config.Project) error {
	res, err := m.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", p.Dir, "rev-parse", "HEAD"},
		Dir:  p.Dir,
	})
	if err != nil {
		return fmt.Errorf("reading the project revision: %w", err)
	}
	meta := Meta{
		GeneratedAt: time.Now().UTC(),
		GeneratedBy: currentUser(),
		GitRev:      strings.TrimSpace(res.Stdout),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.MetaPath(name), append(data, '\n'), 0o644)
}

// migrate replays the migration history in a throwaway container whose memory
// cap is lifted. Running it through `exec` would put the peak inside the
// developer's own backend, where a mem_limit sized for day-to-day work gets it
// OOM-killed, taking the running server down as collateral.
func (m *Manager) migrate(ctx context.Context, p config.Project, cfg config.Backup, db string) error {
	files, err := compose.Files(p.Dir)
	if err != nil {
		return err
	}
	override, err := writeMemOverride(cfg.AppService)
	if err != nil {
		return err
	}
	defer os.Remove(override)

	args := []string{"compose"}
	for _, f := range append(files, override) {
		args = append(args, "-f", f)
	}
	// A fresh container lacks anything the service installs at startup.
	shell := config.Expand(cfg.MigrateCommand, db)
	if cfg.DepsCommand != "" {
		shell = cfg.DepsCommand + " && " + shell
	}
	args = append(args, "run", "--rm", "--no-deps", "-T")
	for _, k := range sortedKeys(cfg.Env) {
		args = append(args, "-e", k+"="+config.Expand(cfg.Env[k], db))
	}
	args = append(args, cfg.AppService, "sh", "-c", shell)

	if _, err := m.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: args, Dir: p.Dir, Live: true}); err != nil {
		return fmt.Errorf("migrations on the temporary database: %w", withOOMHint(err))
	}
	return nil
}

// writeMemOverride lifts the memory cap for the disposable container only:
// mem_limit 0 means unlimited, so the peak is bounded by the Docker VM alone.
func writeMemOverride(service string) (string, error) {
	f, err := os.CreateTemp("", "wtm-mem-*.yaml")
	if err != nil {
		return "", fmt.Errorf("temporary memory override: %w", err)
	}
	_, err = f.WriteString("services:\n  " + service + ":\n    mem_limit: 0\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

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

// sortedKeys keeps the generated command stable across runs.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// List reports every project that has, or is expected to have, a backup.
func (m *Manager) List(cfg *config.Config) ([]Info, error) {
	var infos []Info
	for _, name := range cfg.Names() {
		info := Info{Name: name}
		st, err := os.Stat(m.DumpPath(name))
		switch {
		case err == nil:
			info.Present = true
			info.Size = st.Size()
			if meta, err := m.ReadMeta(name); err == nil {
				info.Meta = meta
			}
		case !os.IsNotExist(err):
			return nil, err
		case !cfg.Projects[name].Dump:
			continue // no dump enabled and nothing on disk: nothing to say
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (m *Manager) ReadMeta(name string) (Meta, error) {
	data, err := os.ReadFile(m.MetaPath(name))
	if err != nil {
		return Meta{}, err
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, fmt.Errorf("%s is unreadable: %w", m.MetaPath(name), err)
	}
	return meta, nil
}

// Remove deletes the dump and its metadata. Removing an absent backup is not an
// error; the boolean says whether anything was actually deleted.
func (m *Manager) Remove(name string) (bool, error) {
	var removed bool
	for _, path := range []string{m.DumpPath(name), m.MetaPath(name)} {
		err := os.Remove(path)
		switch {
		case err == nil:
			removed = true
		case !os.IsNotExist(err):
			return removed, err
		}
	}
	_ = os.Remove(filepath.Dir(m.DumpPath(name)))
	return removed, nil
}

// withOOMHint turns a bare SIGKILL into something actionable. Replaying the
// whole migration history peaks well above what a shared Docker VM has left.
func withOOMHint(err error) error {
	var e *execx.Error
	if errors.As(err, &e) && e.ExitCode == 137 {
		return fmt.Errorf("%w\nprocess killed (exit 137), most likely out of memory: increase the RAM allocated to Docker, or stop non-essential services during the refresh (frontend, celery, pgadmin)", err)
	}
	return err
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

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}
