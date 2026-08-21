package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// A scripted --no-input registration skips the stepper, so the engine
// detection has to run on the flag path too: a mongo project silently
// recorded as postgres only fails at the first refresh.
func TestDetectEngineIfUnsetFillsFromCompose(t *testing.T) {
	dir := t.TempDir()
	body := "services:\n  db:\n    image: mongo:7\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	p := config.Project{Dir: dir, Dump: true, Backup: &config.Backup{AppService: "app"}}
	detectEngineIfUnset(&p)
	if p.Backup.DBEngine != "mongodb" {
		t.Fatalf("engine = %q, the compose image says mongodb", p.Backup.DBEngine)
	}

	explicit := config.Project{Dir: dir, Dump: true, Backup: &config.Backup{DBEngine: "mysql"}}
	detectEngineIfUnset(&explicit)
	if explicit.Backup.DBEngine != "mysql" {
		t.Fatalf("an explicit engine must never be overridden, got %q", explicit.Backup.DBEngine)
	}

	off := config.Project{Dir: dir}
	detectEngineIfUnset(&off)
	if off.Backup != nil {
		t.Fatalf("a project without backup must stay untouched, got %+v", off.Backup)
	}
}
