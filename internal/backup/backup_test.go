package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestRefreshRunsFullSequence(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	p := newProject(t)
	err := m.Refresh(context.Background(), "my-app", p)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	lines := f.Lines()
	wantOrder := []string{
		"compose up -d db",
		"pg_isready",
		"DROP DATABASE IF EXISTS my_app_snapshot_tmp",
		"CREATE DATABASE my_app_snapshot_tmp",
		"manage.py migrate",
		"pg_dump",
	}
	at := 0
	for _, want := range wantOrder {
		found := -1
		for i := at; i < len(lines); i++ {
			if strings.Contains(lines[i], want) {
				found = i
				break
			}
		}
		if found == -1 {
			t.Fatalf("step %q missing or out of order in %v", want, lines)
		}
		at = found
	}
	assertCleanupOfADownedStack(t, lines)
	if !strings.Contains(lines[len(lines)-3], "DROP DATABASE") {
		t.Fatalf("the temporary database must be dropped before the stack comes down, got %q", lines[len(lines)-3])
	}
	for _, c := range f.Calls {
		if strings.Contains(strings.Join(c.Args, " "), "migrate") {
			if !strings.Contains(strings.Join(c.Env, " "), "DB_NAME=my_app_snapshot_tmp") {
				t.Fatalf("migrate must target the temporary database through Env, got %v", c.Env)
			}
		}
		if c.Dir != p.Dir {
			t.Fatalf("commands must run from the project directory, got %q", c.Dir)
		}
	}
}

func TestRefreshStopsWhenStackCannotStart(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "up -d") {
			return execx.Result{}, errors.New("Cannot connect to the Docker daemon")
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	err := m.Refresh(context.Background(), "myapp", newProject(t))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Docker daemon") {
		t.Fatalf("underlying error should be propagated, got %q", err.Error())
	}
	for _, l := range f.Lines() {
		if strings.Contains(l, "psql") || strings.Contains(l, "migrate") || strings.Contains(l, "pg_dump") {
			t.Fatalf("nothing should run after a failed up, got %q", l)
		}
	}
}

func TestRefreshGivesUpWhenPostgresNeverReady(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "pg_isready") {
			return execx.Result{}, errors.New("no response")
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	err := m.Refresh(context.Background(), "myapp", newProject(t))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Fatalf("error should mention database readiness, got %q", err.Error())
	}
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// The database container mounts this directory and runs the restore as its own
// user, never as root: 0700 leaves the dump unreadable on a native docker and
// the worktree comes up silently empty.
func TestProjectDirIsReadableByTheDatabaseContainer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "backups")
	dir, err := ProjectDir(root, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if got := modeOf(t, dir); got != 0o755 {
		t.Fatalf("project directory mode = %o, want 755", got)
	}
	if got := modeOf(t, root); got != 0o700 {
		t.Fatalf("the backups root stays private, mode = %o, want 700", got)
	}
}

// MkdirAll leaves an existing directory's mode alone, and every install before
// this created it 0700.
func TestProjectDirRelaxesADirectoryLeftPrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(filepath.Join(root, "myapp"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := ProjectDir(root, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if got := modeOf(t, dir); got != 0o755 {
		t.Fatalf("an existing directory must be relaxed too, mode = %o", got)
	}
}

func TestRefreshWritesADumpTheContainerCanRead(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := modeOf(t, m.DumpPath("myapp")); got != 0o644 {
		t.Fatalf("dump mode = %o, want 644", got)
	}
}
