package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// The file refresh has no server to talk to: no service start, no probe, no
// temporary database. It migrates into a throwaway file that the app
// service's bind mount surfaces on the host, then collects that file.
func TestRefreshCollectsTheMigratedSQLiteFile(t *testing.T) {
	p := newProject(t)
	p.Backup.DBEngine = "sqlite"
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "manage.py migrate") {
			// What the bind mount does for real: the file the container wrote
			// appears in the project directory.
			if err := os.WriteFile(filepath.Join(p.Dir, ".wtm-snapshot-tmp.sqlite3"), []byte("sqlite-payload"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p.Dir, ".wtm-snapshot-tmp.sqlite3-wal"), []byte("wal"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", p); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	all := strings.Join(f.Lines(), "\n")
	for _, forbidden := range []string{"exec -T", "up -d", "CREATE DATABASE", "DROP DATABASE"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("a file engine must not run %q:\n%s", forbidden, all)
		}
	}
	var migrate execx.Call
	for _, c := range f.Calls {
		if strings.Contains(c.Line(), "manage.py migrate") {
			migrate = c
		}
	}
	if !strings.Contains(strings.Join(migrate.Env, " "), "DB_NAME=.wtm-snapshot-tmp.sqlite3") {
		t.Fatalf("the migration must target the throwaway file, env = %v", migrate.Env)
	}

	dump, err := os.ReadFile(m.DumpPath("myapp"))
	if err != nil || string(dump) != "sqlite-payload" {
		t.Fatalf("dump = %q, %v", dump, err)
	}
	for _, leftover := range []string{".wtm-snapshot-tmp.sqlite3", ".wtm-snapshot-tmp.sqlite3-wal"} {
		if _, err := os.Stat(filepath.Join(p.Dir, leftover)); !os.IsNotExist(err) {
			t.Fatalf("%s must be cleaned up from the project", leftover)
		}
	}
	if _, err := os.Stat(m.MetaPath("myapp")); err != nil {
		t.Fatalf("the meta file must be written: %v", err)
	}
}

// Opening a sqlite database creates the file and writes nothing, so a
// migration that built its schema in another database leaves an empty one
// behind. Publishing it would bring every worktree up on an empty database.
func TestRefreshSQLiteRefusesAnEmptyFile(t *testing.T) {
	p := newProject(t)
	p.Backup.DBEngine = "sqlite"
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "manage.py migrate") {
			if err := os.WriteFile(filepath.Join(p.Dir, ".wtm-snapshot-tmp.sqlite3"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	err := m.Refresh(context.Background(), "myapp", p)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an empty file must be refused, got %v", err)
	}
	if _, statErr := os.Stat(m.DumpPath("myapp")); !os.IsNotExist(statErr) {
		t.Fatal("no dump may be published on a failed refresh")
	}
}

// Without the bind mount the migrated file stays inside the dead container:
// the error has to say what to fix rather than report a missing file.
func TestRefreshSQLiteExplainsAMissingBindMount(t *testing.T) {
	p := newProject(t)
	p.Backup.DBEngine = "sqlite"
	m := newManager(t, &execx.Fake{Handler: okHandler})
	err := m.Refresh(context.Background(), "myapp", p)
	if err == nil || !strings.Contains(err.Error(), "bind-mount") {
		t.Fatalf("the bind-mount requirement must be named, got %v", err)
	}
	if _, statErr := os.Stat(m.DumpPath("myapp")); !os.IsNotExist(statErr) {
		t.Fatal("no dump may be published on a failed refresh")
	}
}
