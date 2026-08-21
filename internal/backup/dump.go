package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// dump streams the engine's dump command to a temporary file and renames it
// only on success, so a failed refresh never leaves a partial dump behind.
func (m *Manager) dump(ctx context.Context, name string, p config.Project, cfg config.Backup, eng dbengine.Engine, db string) error {
	final := m.DumpPath(name)
	// A dump carries everything the migrations create, reference data included,
	// so it stays readable by its owner alone.
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	tmpPath := final + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating the temporary dump: %w", err)
	}
	_, runErr := m.Runner.Run(ctx, execx.Cmd{
		Name:   "docker",
		Args:   append([]string{"compose", "exec", "-T", cfg.DBService}, eng.DumpArgs(cfg.DBUser, db)...),
		Dir:    p.Dir,
		Stdout: tmp,
	})
	closeErr := tmp.Close()
	if runErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("dumping %s: %w", db, runErr)
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
