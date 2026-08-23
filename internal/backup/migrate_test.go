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
	if !strings.Contains(err.Error(), "memory") {
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

	var (
		migrate     string
		migrateCall execx.Call
	)
	for _, c := range f.Calls {
		if l := c.Line(); strings.Contains(l, "manage.py migrate") {
			migrate, migrateCall = l, c
		}
	}
	for _, want := range []string{"run --rm --no-deps", "-e DB_NAME", "backend sh -c"} {
		if !strings.Contains(migrate, want) {
			t.Fatalf("migrate call %q should contain %q", migrate, want)
		}
	}
	if strings.Contains(migrate, "exec") {
		t.Fatalf("migrate must not exec into the running backend: %q", migrate)
	}
	// The value goes through the process environment, never through the
	// arguments: an error message quoting the command must not leak it.
	if strings.Contains(migrate, "myapp_snapshot_tmp") {
		t.Fatalf("the env value must not appear in the arguments: %q", migrate)
	}
	found := false
	for _, kv := range migrateCall.Env {
		if kv == "DB_NAME=myapp_snapshot_tmp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DB_NAME should reach docker through Env, got %v", migrateCall.Env)
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

// The service name lands in a generated compose document loaded with -f:
// a newline would inject arbitrary keys, so it is checked at the point of
// generation too, not only at registration (config.json can be hand-edited).
func TestWriteMemOverrideRejectsANonIdentifierService(t *testing.T) {
	if _, err := writeMemOverride(t.TempDir(), "backend:\n    privileged: true\n  x"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestWriteMemOverrideStaysInTheGivenDirectory(t *testing.T) {
	dir := t.TempDir()
	path, err := writeMemOverride(dir, "backend")
	if err != nil {
		t.Fatalf("writeMemOverride: %v", err)
	}
	defer os.Remove(path)
	if filepath.Dir(path) != dir {
		t.Fatalf("override written to %s, want it under %s", path, dir)
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
