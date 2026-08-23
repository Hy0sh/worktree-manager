package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestListReportsPresentAndMissingBackups(t *testing.T) {
	m := newManager(t, &execx.Fake{})
	if err := os.MkdirAll(filepath.Dir(m.DumpPath("alpha")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.DumpPath("alpha"), []byte("1234567890"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Projects: map[string]config.Project{
		"alpha": {Dir: "/a", Dump: true},
		"beta":  {Dir: "/b", Dump: true},
	}}
	infos, err := m.List(cfg)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(infos))
	}
	if infos[0].Name != "alpha" || !infos[0].Present || infos[0].Size != 10 {
		t.Fatalf("alpha = %+v", infos[0])
	}
	if infos[1].Name != "beta" || infos[1].Present {
		t.Fatalf("beta = %+v", infos[1])
	}
}

func TestRemoveDeletesFilesAndIsSoftWhenAbsent(t *testing.T) {
	m := newManager(t, &execx.Fake{})
	if err := os.MkdirAll(filepath.Dir(m.DumpPath("alpha")), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{m.DumpPath("alpha"), m.MetaPath("alpha")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := m.Remove("alpha")
	if err != nil || !removed {
		t.Fatalf("Remove: removed=%v err=%v", removed, err)
	}
	if _, err := os.Stat(m.DumpPath("alpha")); !os.IsNotExist(err) {
		t.Fatal("dump should be gone")
	}
	removed, err = m.Remove("alpha")
	if err != nil {
		t.Fatalf("removing an absent backup must not be an error, got %v", err)
	}
	if removed {
		t.Fatal("second removal should report nothing removed")
	}
}

func TestRemoveDeletesTheDirectoryDespiteTheRefreshLock(t *testing.T) {
	root := t.TempDir()
	m := &Manager{Root: root}
	dir := filepath.Join(root, "myapp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"myapp.dump", "myapp.dump.meta", "refresh.lock"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := m.Remove("myapp")
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the backup directory should be gone")
	}
}
