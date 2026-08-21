package backup

import (
	"context"
	"errors"
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
	if !strings.Contains(lines[len(lines)-1], "DROP DATABASE") {
		t.Fatalf("the temporary database must be dropped last, got %q", lines[len(lines)-1])
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
