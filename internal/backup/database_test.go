package backup

import (
	"context"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

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
