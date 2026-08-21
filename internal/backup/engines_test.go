package backup

import (
	"context"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// The refresh sequence must go through the project's engine, and no argv may
// carry a credential value: only container-side variable references.
func TestRefreshUsesTheConfiguredEngine(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	p := newProject(t)
	p.Backup.DBEngine = "mysql"
	if err := m.Refresh(context.Background(), "myapp", p); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	all := strings.Join(f.Lines(), "\n")
	for _, want := range []string{
		`mysql -uroot -e "SELECT 1"`,
		"DROP DATABASE IF EXISTS myapp_snapshot_tmp",
		"CREATE DATABASE myapp_snapshot_tmp",
		"mysqldump --single-transaction",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "pg_") {
		t.Fatalf("no postgres command may run on a mysql project:\n%s", all)
	}
}

func TestRefreshSkipsCreateForMongo(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	p := newProject(t)
	p.Backup.DBEngine = "mongodb"
	if err := m.Refresh(context.Background(), "myapp", p); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	all := strings.Join(f.Lines(), "\n")
	if strings.Contains(all, "CREATE DATABASE") {
		t.Fatalf("mongo must not create the temporary database:\n%s", all)
	}
	for _, want := range []string{
		"mongosh --quiet --eval 'db.runCommand({ping:1})'",
		"dropDatabase()",
		"mongodump --archive --db=myapp_snapshot_tmp",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in:\n%s", want, all)
		}
	}
}

func TestRefreshRejectsAnUnknownEngine(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	p := newProject(t)
	p.Backup.DBEngine = "oracle"
	if err := m.Refresh(context.Background(), "myapp", p); err == nil {
		t.Fatal("an unknown engine must fail before touching docker")
	}
	if len(f.Calls) != 0 {
		t.Fatalf("no command should run for an unknown engine, got %v", f.Lines())
	}
}
