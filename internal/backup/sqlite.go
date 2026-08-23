package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// tmpDBFile is the throwaway database the migration writes, relative to the
// project root: the app service bind-mounts the project, which is how the
// file becomes reachable from the host once the container is gone.
const tmpDBFile = ".wtm-snapshot-tmp.sqlite3"

// refreshFile rebuilds the dump of a file-based engine. There is no server to
// start, probe or clean: migrate into a throwaway file inside the disposable
// container, then collect that file as the dump.
func (m *Manager) refreshFile(ctx context.Context, name string, p config.Project, cfg config.Backup) error {
	host := filepath.Join(p.Dir, tmpDBFile)
	removeDBFiles(host)
	defer removeDBFiles(host)

	if err := m.migrate(ctx, p, cfg, tmpDBFile); err != nil {
		return err
	}
	info, err := os.Stat(host)
	if err != nil {
		return fmt.Errorf("the migration left no %s in %s: the %s service must bind-mount the project directory for wtm to collect the database file", tmpDBFile, p.Dir, cfg.AppService)
	}
	// Opening a sqlite database creates the file and writes nothing into it, so
	// an empty file is a migration that connected somewhere and built its
	// schema elsewhere. Publishing it would bring every worktree up on an empty
	// database, which only shows at the first start.
	if info.Size() == 0 {
		return fmt.Errorf("the migrations left %s empty, so its dump would bring every worktree up on an empty database.\n"+
			"`%s` ran, but built nothing there: map the variable the app reads to %s in the project's `backup.env`",
			tmpDBFile, cfg.MigrateCommand, config.DatabasePlaceholder)
	}
	if err := m.publishFile(host, name); err != nil {
		return err
	}
	m.logf("dump written: %s", m.DumpPath(name))
	m.writeMetaOrWarn(ctx, name, p)
	return nil
}

// removeDBFiles drops a sqlite file along with its -wal and -shm siblings.
func removeDBFiles(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
}

// publishFile moves the migrated file into the backups directory the same way
// dump() does: through a .tmp neighbour and a rename, never in place.
func (m *Manager) publishFile(src, name string) error {
	final := m.DumpPath(name)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmpPath := final + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating the temporary dump: %w", err)
	}
	_, copyErr := io.Copy(tmp, in)
	closeErr := tmp.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return copyErr
	}
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publishing the dump: %w", err)
	}
	return nil
}
