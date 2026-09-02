// Package backup produces and manages the pre-migrated database dump shared by
// every worktree of a project.
package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/gofrs/flock"
)

const (
	dbWaitAttempts      = 60 // postgres answers quickly once the container is up
	defaultWaitInterval = time.Second
	// Named here because removing a backup has to know which files it owns,
	// and the restore script is written by another package.
	refreshLockName = "refresh.lock"
	// RestoreScriptName is the docker-entrypoint-initdb.d script each worktree
	// reaches through its .db-snapshot link.
	RestoreScriptName = "restore-snapshot.sh"
)

type Manager struct {
	Runner          execx.Runner
	Root            string
	Out             io.Writer
	MaxWaitAttempts int
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

// ProjectDir is where a project's dump, its meta and the restore script live.
// It is 0755 under a 0700 root on purpose: the database container mounts this
// very directory and runs the restore as its own user, never as root.
func ProjectDir(root, name string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// MkdirAll leaves an existing directory's mode alone, and every install
	// before this created it 0700.
	return dir, os.Chmod(dir, 0o755)
}

// lockRefresh serialises the refreshes of one project: two at once fight over
// the same throwaway database and temporary file. Failing fast beats queueing,
// since waiting behind another refresh only redoes its work.
func (m *Manager) lockRefresh(name string) (func(), error) {
	dir, err := ProjectDir(m.Root, name)
	if err != nil {
		return nil, err
	}
	l := flock.New(filepath.Join(dir, refreshLockName))
	locked, err := l.TryLock()
	if err != nil {
		return nil, fmt.Errorf("taking %s: %w", l.Path(), err)
	}
	if !locked {
		return nil, fmt.Errorf("a refresh of %s is already running; wait for it to finish", name)
	}
	return func() { _ = l.Unlock() }, nil
}

// Refresh regenerates the dump: it starts the database if needed, migrates a
// throwaway one and dumps it as migrate left it. What the migrations create is
// captured, permissions and reference data included; seed data is not.
func (m *Manager) Refresh(ctx context.Context, name string, p config.Project) error {
	cfg := p.BackupConfig()
	if err := cfg.Validate(); err != nil {
		return err
	}
	unlock, err := m.lockRefresh(name)
	if err != nil {
		return err
	}
	defer unlock()
	// A file-based engine has no server: its snapshot is the file itself.
	if dbengine.IsFileBased(cfg.DBEngine) {
		return m.refreshFile(ctx, name, p, cfg)
	}
	eng, err := dbengine.ByName(cfg.DBEngine)
	if err != nil {
		return err
	}
	db := dbengine.TempDBName(name)

	cleanup, err := m.ensureUp(ctx, name, p, cfg)
	if err != nil {
		return err
	}
	// Whatever the refresh started for itself goes back down, whether the dump
	// was written or the migration failed halfway.
	defer cleanup()
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
	if err := m.assertPopulated(ctx, p, cfg, eng, db); err != nil {
		return err
	}

	if err := m.dump(ctx, name, p, cfg, eng, db); err != nil {
		return err
	}
	m.writeMetaOrWarn(ctx, name, p)
	return nil
}

// writeMetaOrWarn settles for a warning when it cannot record the revision:
// the dump is already published, so failing the refresh over the staleness
// heuristic would throw away minutes of migrations for a note.
func (m *Manager) writeMetaOrWarn(ctx context.Context, name string, p config.Project) {
	if err := m.writeMeta(ctx, name, p); err != nil {
		m.logf("warning: the dump is written but its metadata is not, so `backup list` "+
			"cannot say how far behind it is: %v", err)
	}
}
