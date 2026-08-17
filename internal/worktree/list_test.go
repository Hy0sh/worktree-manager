package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

func TestListReportsStackStatus(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	up := stack.ProjectName(filepath.Base(f.root), 1, "feat/x")
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "docker ps") {
			return execx.Result{Stdout: up + "\nsome-other-project\n"}, nil
		}
		return inner(c)
	}
	entries, err := List(context.Background(), f.opts(""))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(entries))
	}
	if entries[0].Status != "up" || entries[0].Branch != "feat/x" {
		t.Fatalf("entry = %+v", entries[0])
	}
}

func TestListReportsDownWhenNoContainerRuns(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "docker ps") {
			return execx.Result{Stdout: "\n"}, nil
		}
		return inner(c)
	}
	entries, err := List(context.Background(), f.opts(""))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if entries[0].Status != "down" {
		t.Fatalf("status = %q, want down", entries[0].Status)
	}
}

// A listing is a question about git: an unresponsive daemon must degrade the
// status column, never make the command fail or hang.
func TestListSurvivesAnUnreachableDocker(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "docker ps") {
			return execx.Result{}, errors.New("Cannot connect to the Docker daemon")
		}
		return inner(c)
	}
	entries, err := List(context.Background(), f.opts(""))
	if err != nil {
		t.Fatalf("List must not fail when docker is down: %v", err)
	}
	if entries[0].Status != StatusUnknown {
		t.Fatalf("status = %q, want %q", entries[0].Status, StatusUnknown)
	}
}

func TestListReportsNoStatusWithoutCompose(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.root, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	entries, err := List(context.Background(), f.opts(""))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if entries[0].Status != StatusUnknown {
		t.Fatalf("status = %q, a project without a stack is neither up nor down", entries[0].Status)
	}
}
