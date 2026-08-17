package backup

import (
	"os"
	"path/filepath"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// Info is one line of `backup list`.
type Info struct {
	Name    string
	Present bool
	Size    int64
	Meta    Meta
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
