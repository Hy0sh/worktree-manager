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

// The listing has been bounded since it existed, so that a wedged daemon
// leaves a `-` in the STATUS column instead of hanging. Nothing held it.
func TestTheListingAsksDockerUnderADeadline(t *testing.T) {
	f := newFixture(t)
	if _, err := List(context.Background(), f.opts("")); err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, c := range f.fake.Calls {
		if c.Name == "docker" && !c.Bounded {
			t.Errorf("`%s` can hang the listing", c.Line())
		}
	}
}

// The worktrees left to adopt are the ones the listing hid, which is exactly
// the set an agent has to name: one to bring a second worktree of its session
// in, one to bring back a worktree whose adoption was released to save space.
func TestListReportsTheWorktreesLeftToAdopt(t *testing.T) {
	f := newFixture(t)
	f.foreign = map[string]string{"worktree-curry": filepath.Join(f.root, ".claude", "worktrees", "curry")}
	entries, err := List(context.Background(), f.opts(""))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected wtm's own worktree and the one left to adopt, got %d: %+v", len(entries), entries)
	}
	adoptable := entries[1]
	if adoptable.Branch != "worktree-curry" || adoptable.Status != StatusAdoptable {
		t.Fatalf("entry = %+v, want branch worktree-curry with status %q", adoptable, StatusAdoptable)
	}
	if adoptable.Index != 0 {
		t.Fatalf("index = %d, a worktree wtm has not adopted holds none", adoptable.Index)
	}
	if entries[0].Branch != "feat/x" || entries[0].Status != "down" {
		t.Fatalf("entry = %+v, wtm's own worktree keeps its stack status", entries[0])
	}
}

func TestListKeepsTheStackStatusOfAnAdoptedWorktree(t *testing.T) {
	f := newFixture(t)
	f.foreign = map[string]string{"worktree-curry": filepath.Join(f.root, ".claude", "worktrees", "curry")}
	f.managed = map[string]bool{"worktree-curry": true}
	entries, err := List(context.Background(), f.opts(""))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if entries[1].Status != "down" {
		t.Fatalf("status = %q, an adopted worktree has a stack and is not offered for adoption", entries[1].Status)
	}
}
