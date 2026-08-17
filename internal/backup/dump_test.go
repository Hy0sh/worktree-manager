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
