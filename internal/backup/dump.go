package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func (m *Manager) dump(ctx context.Context, name string, p config.Project, cfg config.Backup, eng dbengine.Engine, db string) error {
	return m.publish(name, func(w io.Writer) error {
		if _, err := m.Runner.Run(ctx, execx.Cmd{
			Name:   "docker",
			Args:   append([]string{"compose", "exec", "-T", cfg.DBService}, eng.DumpArgs(cfg.DBUser, db)...),
			Dir:    p.Dir,
			Stdout: w,
		}); err != nil {
			return fmt.Errorf("dumping %s: %w", db, err)
		}
		return nil
	})
}

// publish writes through a .tmp neighbour and renames only on success, so a
// failed refresh never leaves a partial dump behind. A dump carries everything
// the migrations create, so it stays readable by its owner alone.
func (m *Manager) publish(name string, write func(io.Writer) error) error {
	final := m.DumpPath(name)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	tmpPath := final + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating the temporary dump: %w", err)
	}
	writeErr := write(tmp)
	if closeErr := tmp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publishing the dump: %w", err)
	}
	m.logf("dump written: %s", final)
	return nil
}
