package index

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// newResolver seeds a registry holding one project and returns a resolver on
// it. dockerLabels drives what the fake docker reports: nil means docker is
// unreachable, an empty slice means reachable but empty.
func newResolver(t *testing.T, indices map[string]int, dockerLabels []string) (*Resolver, string, *execx.Fake) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WithLock(path, func(c *config.Config) error {
		c.Projects["myapp"] = config.Project{Dir: "/repo/my-app", WorktreeIndices: indices}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if dockerLabels == nil {
			return execx.Result{ExitCode: 1}, errors.New("docker is down")
		}
		line := c.String()
		switch {
		case strings.Contains(line, "ps -a"):
			return execx.Result{Stdout: strings.Join(dockerLabels, "\n") + "\n"}, nil
		case strings.Contains(line, "volume ls"):
			// Volumes carry the same projects as k=v label lists.
			out := make([]string, 0, len(dockerLabels))
			for _, l := range dockerLabels {
				out = append(out, "com.docker.compose.version=5.1.0,com.docker.compose.project="+l)
			}
			return execx.Result{Stdout: strings.Join(out, "\n") + "\n"}, nil
		}
		return execx.Result{}, nil
	}}
	return &Resolver{ConfigPath: path, Runner: fake, Name: "myapp", RepoName: "my-app", Out: io.Discard}, path, fake
}

func recorded(t *testing.T, path, branch string) int {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Projects["myapp"].WorktreeIndices[branch]
}

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

// bareResolver seeds an empty registered project and returns a resolver
// wired to a custom fake, for tests that need ps -a and volume ls to answer
// independently (newResolver's fake always mirrors one into the other).
func bareResolver(t *testing.T, handler func(execx.Cmd) (execx.Result, error)) *Resolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WithLock(path, func(c *config.Config) error {
		c.Projects["myapp"] = config.Project{Dir: "/repo/my-app"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &execx.Fake{Handler: handler}
	return &Resolver{ConfigPath: path, Runner: fake, Name: "myapp", RepoName: "my-app", Out: io.Discard}
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
