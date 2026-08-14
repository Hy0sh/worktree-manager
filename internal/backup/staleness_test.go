package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func withMeta(t *testing.T, m *Manager, name, rev string) {
	t.Helper()
	if err := m.writeMetaFile(name, Meta{GitRev: rev}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckCountsMigrationCommitsSinceTheDump(t *testing.T) {
	var pathspec string
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "log --oneline") {
			pathspec = c.Args[len(c.Args)-1]
			return execx.Result{Stdout: "abc1 add table\ndef2 alter column\n"}, nil
		}
		return execx.Result{}, nil
	}}
	m := newManager(t, f)
	withMeta(t, m, "myapp", "deadbeef")

	got := m.Check(context.Background(), "myapp", newProject(t))
	if !got.Known || got.Commits != 2 {
		t.Fatalf("staleness = %+v", got)
	}
	if !got.Behind() {
		t.Fatal("two commits behind should be reported as behind")
	}
	if got.Describe() != "2 commits behind" {
		t.Fatalf("Describe = %q", got.Describe())
	}
	if pathspec != config.DefaultMigrationsPath {
		t.Fatalf("pathspec = %q, want the default %q", pathspec, config.DefaultMigrationsPath)
	}
}

func TestCheckReportsAnUpToDateDump(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: "\n"}, nil
	}}
	m := newManager(t, f)
	withMeta(t, m, "myapp", "deadbeef")

	got := m.Check(context.Background(), "myapp", newProject(t))
	if !got.Known || got.Commits != 0 || got.Behind() {
		t.Fatalf("staleness = %+v", got)
	}
	if got.Describe() != "up to date" {
		t.Fatalf("Describe = %q", got.Describe())
	}
}

// A revision rebased away cannot be compared, and saying so beats guessing.
func TestCheckIsUnknownWithoutAComparableRevision(t *testing.T) {
	m := newManager(t, &execx.Fake{})
	if got := m.Check(context.Background(), "absent", newProject(t)); got.Known {
		t.Fatalf("a missing dump has no staleness, got %+v", got)
	}

	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{}, errors.New("unknown revision")
	}}
	m = newManager(t, f)
	withMeta(t, m, "myapp", "gonerev")
	got := m.Check(context.Background(), "myapp", newProject(t))
	if got.Known {
		t.Fatalf("an unknown revision cannot be compared, got %+v", got)
	}
	if got.Describe() != "unknown" {
		t.Fatalf("Describe = %q", got.Describe())
	}
}

func TestCheckHonoursAConfiguredPath(t *testing.T) {
	var pathspec string
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "log --oneline") {
			pathspec = c.Args[len(c.Args)-1]
		}
		return execx.Result{}, nil
	}}
	m := newManager(t, f)
	withMeta(t, m, "myapp", "deadbeef")
	p := newProject(t)
	p.Backup.MigrationsPath = "prisma/migrations/*"

	m.Check(context.Background(), "myapp", p)
	if pathspec != "prisma/migrations/*" {
		t.Fatalf("pathspec = %q", pathspec)
	}
}
