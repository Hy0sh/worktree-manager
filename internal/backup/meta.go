package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Meta travels next to the dump so `backup list` can say how old it is.
type Meta struct {
	GeneratedAt time.Time `json:"generated_at"`
	GitRev      string    `json:"git_rev"`
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
	return m.writeMetaFile(name, Meta{
		GeneratedAt: time.Now().UTC(),
		GitRev:      strings.TrimSpace(res.Stdout),
	})
}

func (m *Manager) writeMetaFile(name string, meta Meta) error {
	if err := os.MkdirAll(filepath.Dir(m.MetaPath(name)), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.MetaPath(name), append(data, '\n'), 0o600)
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
