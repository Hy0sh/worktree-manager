// Package backup produces and manages the pre-migrated Postgres dump shared by
// every worktree of a project.
package backup

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

const (
	dbWaitAttempts      = 60 // postgres answers quickly once the container is up
	defaultWaitInterval = time.Second
)

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

// Refresh regenerates the dump: it starts the database if needed, migrates a
// throwaway one and dumps it as migrate left it, data included. Everything the
// migrations create is therefore captured, permissions and reference data
// among them; only what they did not create, seed data first of all, is out.
func (m *Manager) Refresh(ctx context.Context, name string, p config.Project) error {
	cfg := p.BackupConfig()
	if err := cfg.Validate(); err != nil {
		return err
	}
	// A file-based engine has no server: its snapshot is the file itself.
	if dbengine.IsFileBased(cfg.DBEngine) {
		return m.refreshFile(ctx, name, p, cfg)
	}
	eng, err := dbengine.ByName(cfg.DBEngine)
	if err != nil {
		return err
	}
	db := dbengine.TempDBName(name)

	if err := m.ensureUp(ctx, name, p, cfg); err != nil {
		return err
	}
	if err := m.waitFor(ctx, "the database", dbWaitAttempts, execx.Cmd{
		Name: "docker",
		Args: append([]string{"compose", "exec", "-T", cfg.DBService}, eng.ReadyArgs(cfg.DBUser)...),
		Dir:  p.Dir,
	}); err != nil {
		return err
	}
	// From here the throwaway database may exist, so always try to drop it.
	defer func() {
		_, _ = m.execInDB(ctx, p, cfg, eng.DropTempDBArgs(cfg.DBUser, db))
	}()

	if _, err := m.execInDB(ctx, p, cfg, eng.DropTempDBArgs(cfg.DBUser, db)); err != nil {
		return fmt.Errorf("cleaning up the temporary database: %w", err)
	}
	// Some engines (mongo) create a database on its first write instead.
	if args := eng.CreateTempDBArgs(cfg.DBUser, db); args != nil {
		if _, err := m.execInDB(ctx, p, cfg, args); err != nil {
			return fmt.Errorf("creating the temporary database: %w", err)
		}
	}
	if err := m.migrate(ctx, p, cfg, db); err != nil {
		return err
	}

	if err := m.dump(ctx, name, p, cfg, eng, db); err != nil {
		return err
	}
	return m.writeMeta(ctx, name, p)
}
