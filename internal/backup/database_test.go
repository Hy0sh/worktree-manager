package backup

import (
	"context"
	"errors"
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

// Nothing of the stack was running, so wtm started the database for itself.
// Leaving it up afterwards is a container the developer never asked for, and
// doctor then counts a stack running that nobody started.
func TestRefreshTakesDownWhatItStartedWhenNothingWasThere(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps -a --services") || strings.Contains(c.String(), "ps --services") {
			return execx.Result{}, nil // no container at all, running or stopped
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	assertCleanupOfADownedStack(t, f.Lines())
}

// The db container and its anonymous volumes are wtm's to remove, and the
// network goes with the `down`; a named volume the developer's stack declares
// holds their data even with no container left, so it stays.
func assertCleanupOfADownedStack(t *testing.T, lines []string) {
	t.Helper()
	if n := len(lines); n < 2 || !strings.HasSuffix(lines[n-2], "compose rm -f -s -v db") || !strings.HasSuffix(lines[n-1], "compose down") {
		t.Fatalf("cleanup must remove the db container then the network, ran %v", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "--volumes") {
			t.Fatalf("a named volume of the developer's stack would go with it: %q", l)
		}
	}
}

// Other containers of the stack exist, stopped: the developer owns the stack.
// Only the service wtm started goes back, and its container with it, so `up`
// does not find a half of wtm's making next time.
func TestRefreshRemovesOnlyTheServiceItStartedWhenOthersExist(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "ps -a --services"):
			return execx.Result{Stdout: "backend\nfrontend\n"}, nil
		case strings.Contains(c.String(), "ps --services"):
			return execx.Result{}, nil // nothing running
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	var rm bool
	for _, l := range f.Lines() {
		if strings.HasSuffix(l, "compose down") {
			t.Fatalf("the developer's stopped containers are not wtm's to remove: %q", l)
		}
		if strings.Contains(l, "compose rm -f -s -v db") {
			rm = true
		}
	}
	if !rm {
		t.Fatalf("the db wtm started must be stopped and removed, ran %v", f.Lines())
	}
}

// A stopped db container is the developer's: up reuses it, along with the
// data its anonymous volume holds. It goes back to stopped, nothing removed.
func TestRefreshStopsADatabaseItOnlyRestarted(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "ps -a --services"):
			return execx.Result{Stdout: "db\nbackend\n"}, nil
		case strings.Contains(c.String(), "ps --services"):
			return execx.Result{}, nil
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	var stopped bool
	for _, l := range f.Lines() {
		if strings.Contains(l, "compose rm ") || strings.HasSuffix(l, "compose down") || strings.Contains(l, "down --volumes") {
			t.Fatalf("the developer's container and data must survive: %q", l)
		}
		if strings.HasSuffix(l, "compose stop db") {
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("the db wtm restarted must be stopped again, ran %v", f.Lines())
	}
}

func TestRefreshLeavesARunningDatabaseAlone(t *testing.T) {
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
		if strings.HasSuffix(l, "compose down") || strings.Contains(l, "compose rm ") {
			t.Fatalf("nothing wtm did not start may be touched: %q", l)
		}
	}
}

// A failed refresh used to leave its half-created container and network. The
// cleanup runs on every exit.
func TestRefreshCleansUpAfterAFailedMigration(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "ps -a --services"), strings.Contains(c.String(), "ps --services"):
			return execx.Result{}, nil
		case strings.Contains(c.String(), "run --rm --no-deps"):
			return execx.Result{ExitCode: 1}, errors.New("migration failed")
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err == nil {
		t.Fatal("expected the migration failure")
	}
	assertCleanupOfADownedStack(t, f.Lines())
}

// A failed `compose up` can still leave a created container and its network
// behind, measured on a real machine, so the cleanup must run then too.
func TestRefreshCleansUpAfterAFailedStart(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "ps -a --services"), strings.Contains(c.String(), "ps --services"):
			return execx.Result{}, nil
		case strings.Contains(c.String(), "up -d"):
			return execx.Result{ExitCode: 1}, errors.New("port is already allocated")
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err == nil {
		t.Fatal("expected the start failure")
	}
	assertCleanupOfADownedStack(t, f.Lines())
}

// `down --volumes` removes the named volumes the compose file declares, which
// is where a developer whose stack is simply down keeps their database.
func TestRefreshNeverTakesNamedVolumesDown(t *testing.T) {
	for _, tc := range []struct{ name, existing string }{
		{"nothing there", ""},
		{"the db is stopped", "db\nbackend\n"},
		{"other services are stopped", "backend\nfrontend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
				switch {
				case strings.Contains(c.String(), "ps -a --services"):
					return execx.Result{Stdout: tc.existing}, nil
				case strings.Contains(c.String(), "ps --services"):
					return execx.Result{}, nil
				}
				return okHandler(c)
			}}
			m := newManager(t, f)
			if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
			for _, l := range f.Lines() {
				if strings.Contains(l, "down --volumes") {
					t.Fatalf("the developer's data must survive the cleanup: %q", l)
				}
			}
		})
	}
}
