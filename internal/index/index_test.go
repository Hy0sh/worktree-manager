package index

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestResolveReturnsTheRecordedIndexWithoutTouchingDocker(t *testing.T) {
	r, _, fake := newResolver(t, map[string]int{"feat/x": 7}, nil)
	got, err := r.Resolve(context.Background(), "feat/x", 1, MustExist)
	if err != nil || got != 7 {
		t.Fatalf("got %d, %v", got, err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("the nominal path must not shell out, ran: %v", fake.Lines())
	}
}

func TestResolveFallsBackToTheGitPositionWhenClean(t *testing.T) {
	r, path, _ := newResolver(t, nil, []string{})
	got, err := r.Resolve(context.Background(), "feat/old", 2, MustExist)
	if err != nil || got != 2 {
		t.Fatalf("got %d, %v", got, err)
	}
	if recorded(t, path, "feat/old") != 2 {
		t.Fatal("the fallback must persist too")
	}
}

func TestResolveAllocatesSkippingRecordedAndDirtyIndices(t *testing.T) {
	r, path, _ := newResolver(t, map[string]int{"a": 1, "b": 2}, []string{"my-app-wt-3-dead"})
	got, err := r.Resolve(context.Background(), "c", 0, MayAllocate)
	if err != nil || got != 4 {
		t.Fatalf("1 and 2 are recorded, 3 has debris: expected 4, got %d, %v", got, err)
	}
	if recorded(t, path, "c") != 4 {
		t.Fatal("allocation must persist")
	}
}

func TestResolveMustExistNeverInvents(t *testing.T) {
	r, path, _ := newResolver(t, nil, []string{})
	_, err := r.Resolve(context.Background(), "ghost", 0, MustExist)
	if !errors.Is(err, ErrNoIndex) {
		t.Fatalf("expected ErrNoIndex, got %v", err)
	}
	if recorded(t, path, "ghost") != 0 {
		t.Fatal("MustExist must not write anything")
	}
}

func TestResolveSurvivesAnUnreachableDocker(t *testing.T) {
	r, path, _ := newResolver(t, nil, nil)
	got, err := r.Resolve(context.Background(), "feat/x", 0, MayAllocate)
	if err != nil || got != 1 {
		t.Fatalf("docker being down must degrade to config-only allocation, got %d, %v", got, err)
	}
	if recorded(t, path, "feat/x") != 0 {
		t.Fatal("a resolution made blind, without docker evidence, must not be persisted")
	}
}

func TestResolveWithDockerDownLeavesNoTraceForTheNextCall(t *testing.T) {
	r, path, fake := newResolver(t, nil, nil)
	if _, err := r.Resolve(context.Background(), "feat/x", 0, MayAllocate); err != nil {
		t.Fatal(err)
	}
	if recorded(t, path, "feat/x") != 0 {
		t.Fatal("the blind resolution must not have recorded anything")
	}
	// Docker is back, with no trace of feat/x: a second Resolve must be free
	// to allocate normally and persist it, unconstrained by the earlier
	// in-memory guess.
	fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		switch {
		case strings.Contains(line, "ps -a"):
			return execx.Result{Stdout: "\n"}, nil
		case strings.Contains(line, "volume ls"):
			return execx.Result{Stdout: "\n"}, nil
		}
		return execx.Result{}, nil
	}
	got, err := r.Resolve(context.Background(), "feat/x", 0, MayAllocate)
	if err != nil || got != 1 {
		t.Fatalf("got %d, %v", got, err)
	}
	if recorded(t, path, "feat/x") != 1 {
		t.Fatal("with docker back, the allocation must be recorded for real")
	}
}

func TestReleaseForgetsTheBranch(t *testing.T) {
	r, path, _ := newResolver(t, map[string]int{"feat/x": 5}, []string{})
	if err := r.Release("feat/x"); err != nil {
		t.Fatal(err)
	}
	if recorded(t, path, "feat/x") != 0 {
		t.Fatal("release must delete the entry")
	}
	// The freed index is reusable once nothing in docker carries it.
	got, err := r.Resolve(context.Background(), "feat/y", 0, MayAllocate)
	if err != nil || got != 1 {
		t.Fatalf("got %d, %v", got, err)
	}
}

// A daemon that accepts a connection and never answers used to hang every
// command that resolves an index, with nothing on screen: `wtm list` bounded
// the same question from the start, resolution never did. Resolution already
// knows how to degrade to the registry when docker refuses, so a deadline
// costs it nothing.
func TestDockerIsAskedUnderADeadline(t *testing.T) {
	r, _, fake := newResolver(t, nil, []string{})
	if _, err := r.Resolve(context.Background(), "feat/x", 0, MayAllocate); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	asked := false
	for _, c := range fake.Calls {
		if c.Name != "docker" {
			continue
		}
		asked = true
		if !c.Bounded {
			t.Errorf("`%s` can hang forever", c.Line())
		}
	}
	if !asked {
		t.Fatal("this resolution should have had to ask docker")
	}
}

// The allocator already steps over an index docker holds leftovers for. A
// caller who knows the ports can tell it to step over an index whose ports
// would clash with a recorded neighbour, which is how two worktrees of one
// project stop fighting over 26434.
func TestResolveSkipsAnIndexTheCallerRefuses(t *testing.T) {
	r, path, _ := newResolver(t, map[string]int{"feat/a": 1}, []string{})
	var out strings.Builder
	r.Out = &out
	r.Conflicts = func(n int) string {
		if n == 2 {
			return "db would publish 26434, which feat/a already publishes for db_test"
		}
		return ""
	}
	got, err := r.Resolve(context.Background(), "feat/b", 0, MayAllocate)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != 3 {
		t.Fatalf("index 2 clashes, expected 3, got %d", got)
	}
	if recorded(t, path, "feat/b") != 3 {
		t.Fatal("the skipped index must not be recorded")
	}
	if !strings.Contains(out.String(), "index 2 skipped") || !strings.Contains(out.String(), "26434") {
		t.Fatalf("the skip must say why:\n%s", out.String())
	}
}

func TestResolveWithoutConflictsBehavesAsBefore(t *testing.T) {
	r, _, _ := newResolver(t, map[string]int{"feat/a": 1}, []string{})
	got, err := r.Resolve(context.Background(), "feat/b", 0, MayAllocate)
	if err != nil || got != 2 {
		t.Fatalf("expected 2, got %d (%v)", got, err)
	}
}

// A brand-new worktree's git position is usually free, so the position
// fallback, meant for worktrees older than recorded indices, is what a plain
// create goes through. It has to ask the same question as the allocation loop,
// or two db ports one apart collide on the second create of every project.
func TestResolveFallbackAsksTheCallerToo(t *testing.T) {
	r, path, _ := newResolver(t, map[string]int{"feat/a": 1}, []string{})
	var out strings.Builder
	r.Out = &out
	r.Conflicts = func(n int) string {
		if n == 2 {
			return "db would publish 26434, which feat/a already publishes for db_test"
		}
		return ""
	}
	got, err := r.Resolve(context.Background(), "feat/b", 2, MayAllocate)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != 3 || recorded(t, path, "feat/b") != 3 {
		t.Fatalf("position 2 clashes, expected 3 recorded, got %d", got)
	}
	if !strings.Contains(out.String(), "index 2 skipped") {
		t.Fatalf("the skip must be said:\n%s", out.String())
	}
}
