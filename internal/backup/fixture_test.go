package backup

import (
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
