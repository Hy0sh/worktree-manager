package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	discard := func(string, ...any) {}

	p := config.Project{Dir: dir, Dump: true, Backup: &config.Backup{AppService: "app"}}
	detectEngineIfUnset(&p, discard)
	if p.Backup.DBEngine != "mongodb" {
		t.Fatalf("engine = %q, the compose image says mongodb", p.Backup.DBEngine)
	}

	explicit := config.Project{Dir: dir, Dump: true, Backup: &config.Backup{DBEngine: "mysql"}}
	detectEngineIfUnset(&explicit, discard)
	if explicit.Backup.DBEngine != "mysql" {
		t.Fatalf("an explicit engine must never be overridden, got %q", explicit.Backup.DBEngine)
	}

	off := config.Project{Dir: dir}
	detectEngineIfUnset(&off, discard)
	if off.Backup != nil {
		t.Fatalf("a project without backup must stay untouched, got %+v", off.Backup)
	}
}

// An unrecognised database image must not become postgres silently: the
// warning is what a --no-input caller gets instead of the stepper's question.
func TestDetectEngineWarnsWhenTheImageIsUnknown(t *testing.T) {
	dir := t.TempDir()
	body := "services:\n  db:\n    image: internal-registry/acme-dbproxy:1.2\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := config.Project{Dir: dir, Dump: true}
	var warned []string
	detectEngineIfUnset(&p, func(format string, args ...any) {
		warned = append(warned, fmt.Sprintf(format, args...))
	})
	if p.Backup != nil && p.Backup.DBEngine != "" {
		t.Fatalf("no engine should be recorded, got %q", p.Backup.DBEngine)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "acme-dbproxy") {
		t.Fatalf("expected one warning naming the image, got %v", warned)
	}
}
