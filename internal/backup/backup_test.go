package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func newManager(t *testing.T, f *execx.Fake) *Manager {
	t.Helper()
	return &Manager{Runner: f, Root: t.TempDir(), Out: io.Discard, MaxWaitAttempts: 3}
}

func newProject(t *testing.T) config.Project {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"compose.yaml", "compose.override.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("services:\n  backend: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return config.Project{Dir: dir, Backup: &config.Backup{
		AppService:     "backend",
		MigrateCommand: "python manage.py migrate",
		Env:            map[string]string{"DB_NAME": config.DatabasePlaceholder},
	}}
}

func okHandler(c execx.Cmd) (execx.Result, error) {
	if strings.Contains(c.String(), "pg_dump") {
		return execx.Result{Stdout: "PGDMP-binary-payload"}, nil
	}
	if strings.Contains(c.String(), "rev-parse HEAD") {
		return execx.Result{Stdout: "deadbeefcafe\n"}, nil
	}
	return execx.Result{}, nil
}

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
	if !strings.Contains(lines[len(lines)-1], "DROP DATABASE") {
		t.Fatalf("the temporary database must be dropped last, got %q", lines[len(lines)-1])
	}
	for _, c := range f.Calls {
		if strings.Contains(strings.Join(c.Args, " "), "migrate") {
			if !strings.Contains(strings.Join(c.Args, " "), "DB_NAME=my_app_snapshot_tmp") {
				t.Fatalf("migrate must target the temporary database, got %v", c.Args)
			}
		}
		if c.Dir != p.Dir {
			t.Fatalf("commands must run from the project directory, got %q", c.Dir)
		}
	}
}

// Recreating a container that is already running disrupts the developer's
// stack: an `up -d` on a healthy service costs a full restart for nothing.
func TestRefreshLeavesRunningServicesAlone(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps --services") {
			return execx.Result{Stdout: "db\n"}, nil
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	for _, l := range f.Lines() {
		if strings.Contains(l, "up -d") {
			t.Fatalf("nothing should be started when db and backend already run, got %q", l)
		}
	}
}

func TestRefreshStartsOnlyTheMissingServices(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps --services") {
			return execx.Result{Stdout: "backend\nfrontend\n"}, nil
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	var up string
	for _, l := range f.Lines() {
		if strings.Contains(l, "up -d") {
			up = l
		}
	}
	if !strings.HasSuffix(up, "up -d db") {
		t.Fatalf("only the database should be started, got %q", up)
	}
}

// Since migrations run in their own container, the application service never
// has to be running: the refresh works on a stack that is entirely down except
// the database, and never disturbs a developer's app container.
func TestRefreshNeverStartsNorProbesTheAppService(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps --services") {
			return execx.Result{Stdout: "db\n"}, nil
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	for _, l := range f.Lines() {
		if strings.Contains(l, "exec") && strings.Contains(l, "backend") {
			t.Fatalf("the app container must not be touched, got %q", l)
		}
		if strings.Contains(l, "import django") {
			t.Fatalf("no framework-specific probe should remain, got %q", l)
		}
	}
}

// Replaying the full migration history is the memory-hungriest step; on a
// Docker VM sized for the running stack it gets the backend OOM-killed.
func TestRefreshExplainsAnOOMKill(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "manage.py migrate") {
			return execx.Result{}, &execx.Error{Cmd: c.String(), ExitCode: 137, Err: errors.New("killed")}
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	err := m.Refresh(context.Background(), "myapp", newProject(t))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "mémoire") {
		t.Fatalf("exit 137 should be explained as a memory problem, got %q", err.Error())
	}
}

// Migrating through `exec` puts the peak inside the developer's own backend,
// where a mem_limit sized for day-to-day work gets it OOM-killed and takes the
// running server down with it. A disposable container with no cap keeps the
// blast radius on something we own.
func TestRefreshMigratesInADisposableContainer(t *testing.T) {
	var override string
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "manage.py migrate") {
			var last string
			for i := 0; i < len(c.Args)-1; i++ {
				if c.Args[i] == "-f" {
					last = c.Args[i+1]
				}
			}
			data, err := os.ReadFile(last)
			if err != nil {
				t.Errorf("the temporary override should exist while the command runs: %v", err)
			}
			override = string(data)
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	p := newProject(t)
	if err := m.Refresh(context.Background(), "myapp", p); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	var migrate string
	for _, l := range f.Lines() {
		if strings.Contains(l, "manage.py migrate") {
			migrate = l
		}
	}
	for _, want := range []string{"run --rm --no-deps", "-e DB_NAME=myapp_snapshot_tmp", "backend sh -c"} {
		if !strings.Contains(migrate, want) {
			t.Fatalf("migrate call %q should contain %q", migrate, want)
		}
	}
	if strings.Contains(migrate, "exec") {
		t.Fatalf("migrate must not exec into the running backend: %q", migrate)
	}
	for _, want := range []string{filepath.Join(p.Dir, "compose.yaml"), filepath.Join(p.Dir, "compose.override.yaml")} {
		if !strings.Contains(migrate, want) {
			t.Fatalf("the project compose files must be passed explicitly, missing %q in %q", want, migrate)
		}
	}
	if !strings.Contains(override, "mem_limit: 0") || !strings.Contains(override, "backend") {
		t.Fatalf("the override should lift the backend memory cap, got %q", override)
	}
}

// Dependencies installed at container startup do not exist in a fresh one.
func TestRefreshRunsTheDepsCommandFirst(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	p := newProject(t)
	p.Backup.DepsCommand = "poetry install --no-root --with dev"
	if err := m.Refresh(context.Background(), "myapp", p); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	var migrate string
	for _, l := range f.Lines() {
		if strings.Contains(l, "manage.py migrate") {
			migrate = l
		}
	}
	if !strings.Contains(migrate, "poetry install --no-root --with dev && python manage.py migrate") {
		t.Fatalf("deps command should run before migrate, got %q", migrate)
	}
}

func TestRefreshWritesDumpAndMeta(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	data, err := os.ReadFile(m.DumpPath("myapp"))
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	if string(data) != "PGDMP-binary-payload" {
		t.Fatalf("dump content = %q", data)
	}
	meta, err := m.ReadMeta("myapp")
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.GitRev != "deadbeefcafe" {
		t.Fatalf("git_rev = %q", meta.GitRev)
	}
	if meta.GeneratedBy == "" || meta.GeneratedAt.IsZero() {
		t.Fatalf("meta incomplete: %+v", meta)
	}
	if _, err := os.Stat(m.DumpPath("myapp") + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("the temporary dump file should be gone")
	}
}

func TestRefreshKeepsPreviousDumpWhenPgDumpFails(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "pg_dump") {
			return execx.Result{}, errors.New("pg_dump: connection lost")
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := os.MkdirAll(filepath.Dir(m.DumpPath("myapp")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.DumpPath("myapp"), []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := m.Refresh(context.Background(), "myapp", newProject(t))
	if err == nil {
		t.Fatal("expected an error")
	}
	data, _ := os.ReadFile(m.DumpPath("myapp"))
	if string(data) != "previous" {
		t.Fatalf("the previous dump must survive a failed refresh, got %q", data)
	}
	if _, err := os.Stat(m.DumpPath("myapp") + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("the partial dump should have been cleaned up")
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
	if !strings.Contains(err.Error(), "base de données") {
		t.Fatalf("error should mention database readiness, got %q", err.Error())
	}
}

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
