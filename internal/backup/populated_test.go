package backup

import (
	"context"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// A migration that ran against another database than the throwaway one leaves
// it empty. Dumping it anyway is what published a snapshot bringing every
// worktree up on an empty database, discovered only at the first start.
func TestRefreshRefusesAnEmptyThrowawayDatabase(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "information_schema.tables") {
			return execx.Result{Stdout: "0\n"}, nil
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	err := m.Refresh(context.Background(), "myapp", newProject(t))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"empty", "myapp_snapshot_tmp", "backup.env"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q, got: %v", want, err)
		}
	}
	if strings.Contains(strings.Join(f.Lines(), "\n"), "pg_dump") {
		t.Fatal("nothing should be dumped once the database is known to be empty")
	}
}

func TestRefreshDumpsAPopulatedDatabase(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "information_schema.tables") {
			return execx.Result{Stdout: "42\n"}, nil
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !strings.Contains(strings.Join(f.Lines(), "\n"), "pg_dump") {
		t.Fatalf("the dump should have run, calls:\n%s", strings.Join(f.Lines(), "\n"))
	}
}

// The count is a safety net, not a gate: a probe wtm cannot run, or whose
// output it cannot read, must not fail a refresh that is otherwise fine.
func TestRefreshDumpsWhenTheCountCannotBeRead(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "information_schema.tables") {
			return execx.Result{Stdout: "psql: could not connect\n"}, nil
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("an unreadable count must not fail the refresh: %v", err)
	}
	if !strings.Contains(strings.Join(f.Lines(), "\n"), "pg_dump") {
		t.Fatal("the dump should still have run")
	}
}
