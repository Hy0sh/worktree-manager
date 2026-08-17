package index

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestResolveBackfillsFromAContainerLabel(t *testing.T) {
	r, path, _ := newResolver(t, nil, []string{"my-app-wt-3-review-gal-1020", "other-project"})
	got, err := r.Resolve(context.Background(), "review-gal-1020", 1, MustExist)
	if err != nil || got != 3 {
		t.Fatalf("got %d, %v", got, err)
	}
	if recorded(t, path, "review-gal-1020") != 3 {
		t.Fatal("backfill must persist what docker reported")
	}
}

func TestResolveBackfillMatchesTheSanitizedBranch(t *testing.T) {
	r, _, _ := newResolver(t, nil, []string{"my-app-wt-2-feat-gal-667"})
	got, err := r.Resolve(context.Background(), "feat/gal-667", 9, MustExist)
	if err != nil || got != 2 {
		t.Fatalf("a slashed branch must match its sanitized label, got %d, %v", got, err)
	}
}

func TestResolveBackfillCollisionIsAnActionableError(t *testing.T) {
	r, _, _ := newResolver(t, map[string]int{"review-gal-1020": 3}, []string{"my-app-wt-3-fix-allow"})
	_, err := r.Resolve(context.Background(), "fix-allow", 2, MayAllocate)
	if err == nil {
		t.Fatal("two branches on the same index is a broken state, not something to paper over")
	}
	for _, want := range []string{"docker compose -p my-app-wt-3-fix-allow down -v", "review-gal-1020", "wtm start"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must mention %q", err, want)
		}
	}
}

func TestResolveGitPositionIsSkippedWhenAnotherBranchLeftDebris(t *testing.T) {
	r, _, _ := newResolver(t, nil, []string{"my-app-wt-2-dead-branch"})
	got, err := r.Resolve(context.Background(), "feat/new", 2, MayAllocate)
	if err != nil {
		t.Fatal(err)
	}
	if got == 2 {
		t.Fatal("index 2 still has another branch's containers or volumes; reusing it would inherit them")
	}
	if got != 1 {
		t.Fatalf("expected the first clean free index (1), got %d", got)
	}
}

func TestResolveBackfillsFromAVolumeLabelWhenContainersAreGone(t *testing.T) {
	r := bareResolver(t, func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "ps -a"):
			// No running or stopped containers: only the volume survived.
			return execx.Result{Stdout: "\n"}, nil
		case strings.Contains(c.String(), "volume ls"):
			// com.docker.compose.project deliberately is not the first key,
			// so the order-agnostic k=v parse is what makes this pass.
			return execx.Result{Stdout: "com.docker.compose.version=5.1.0,com.docker.compose.project=my-app-wt-3-review-gal-1020\n"}, nil
		}
		return execx.Result{}, nil
	})
	got, err := r.Resolve(context.Background(), "review-gal-1020", 1, MustExist)
	if err != nil || got != 3 {
		t.Fatalf("got %d, %v", got, err)
	}
}

func TestResolveAllocationSkipsADirtyIndexEvenWhenVolumeListFails(t *testing.T) {
	r := bareResolver(t, func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "ps -a"):
			// Index 1 is still occupied by another branch's container.
			return execx.Result{Stdout: "my-app-wt-1-dead-branch\n"}, nil
		case strings.Contains(c.String(), "volume ls"):
			return execx.Result{}, errors.New("volume ls failed")
		}
		return execx.Result{}, nil
	})
	got, err := r.Resolve(context.Background(), "feat/new", 0, MayAllocate)
	if err != nil {
		t.Fatal(err)
	}
	if got == 1 {
		t.Fatal("index 1 has a container leftover seen via ps -a alone; a failed volume probe must not erase that evidence")
	}
	if got != 2 {
		t.Fatalf("expected the first clean index after skipping the dirty one, got %d", got)
	}
}

func TestMatchBranch(t *testing.T) {
	labels := []string{"noise", "my-app-wt-12-feat-x", "my-app-wt-3-feat-xy"}
	n, label, ok := MatchBranch(labels, "my-app", "feat/x")
	if !ok || n != 12 || label != "my-app-wt-12-feat-x" {
		t.Fatalf("got %d %q %v", n, label, ok)
	}
	if _, _, ok := MatchBranch(labels, "my-app", "feat/z"); ok {
		t.Fatal("no label carries feat/z")
	}
}
