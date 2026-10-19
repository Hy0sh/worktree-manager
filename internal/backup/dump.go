package backup

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// dump writes the throwaway database as the main dump, and each extra database
// under its own name as it stands on the server.
func (m *Manager) dump(ctx context.Context, name string, p config.Project, cfg config.Backup, eng dbengine.Engine, db string) error {
	dbs := append([]string{db}, cfg.ExtraDatabases...)
	paths := []string{m.DumpPath(name)}
	for _, extra := range cfg.ExtraDatabases {
		paths = append(paths, ExtraDumpPath(m.Root, name, extra))
	}
	return m.publish(name, paths, func(w io.Writer, i int) error {
		if _, err := m.Runner.Run(ctx, execx.Cmd{
			Name:   "docker",
			Args:   append([]string{"compose", "exec", "-T", cfg.DBService}, eng.DumpArgs(cfg.DBUser, dbs[i])...),
			Dir:    p.Dir,
			Stdout: w,
		}); err != nil {
			return fmt.Errorf("dumping %s: %w", dbs[i], err)
		}
		return nil
	})
}

// publish writes each file through a .tmp neighbour and renames them only once
// all are written, so a failed refresh never leaves a partial dump behind, nor
// a main dump paired with extra ones from another run. The mode matches the
// directory's: the database container reads these files as its own user.
func (m *Manager) publish(name string, finals []string, write func(w io.Writer, i int) error) error {
	if _, err := ProjectDir(m.Root, name); err != nil {
		return err
	}
	var tmps []string
	// After the renames there is nothing left to remove.
	defer func() {
		for _, tmp := range tmps {
			_ = os.Remove(tmp)
		}
	}()
	for i, final := range finals {
		tmpPath := final + ".tmp"
		tmps = append(tmps, tmpPath)
		tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("creating the temporary dump: %w", err)
		}
		writeErr := write(tmp, i)
		if closeErr := tmp.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return writeErr
		}
	}
	for i, final := range finals {
		if err := os.Rename(tmps[i], final); err != nil {
			return fmt.Errorf("publishing the dump: %w", err)
		}
		m.logf("dump written: %s", final)
	}
	return nil
}
