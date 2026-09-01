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

// refreshFile has no server to start, probe or clean: it migrates into a
// throwaway file inside the disposable container, then collects it as the dump.
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
	// an empty file is a migration that built its schema somewhere else, and
	// publishing it would bring every worktree up empty.
	if info.Size() == 0 {
		return fmt.Errorf("the migrations left %s empty, so its dump would bring every worktree up on an empty database.\n"+
			"`%s` ran, but built nothing there: map the variable the app reads to %s in the project's `backup.env`",
			tmpDBFile, cfg.MigrateCommand, config.DatabasePlaceholder)
	}
	if err := m.publishFile(host, name); err != nil {
		return err
	}
	m.writeMetaOrWarn(ctx, name, p)
	return nil
}

// removeDBFiles drops a sqlite file along with its -wal and -shm siblings.
func removeDBFiles(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
}

func (m *Manager) publishFile(src, name string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return m.publish(name, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}
