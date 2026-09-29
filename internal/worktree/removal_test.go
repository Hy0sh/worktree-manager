package worktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// dirty has git report a tracked change in every worktree it is asked about.
func dirty(f *fixture) {
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "status --porcelain") {
			return execx.Result{Stdout: " M backend/models.py\n"}, nil
		}
		return inner(c)
	}
}

func recordIndex(t *testing.T, f *fixture, branch string, n int) {
	t.Helper()
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		p := c.Projects["myapp"]
		p.WorktreeIndices = map[string]int{branch: n}
		c.Projects["myapp"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The plan is shown before anybody answers, so finding it must not take
// anything down or write anything, whatever it finds.
func assertNothingChanged(t *testing.T, f *fixture) {
	t.Helper()
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, " down") || strings.Contains(l, "worktree remove") ||
			strings.HasPrefix(l, "docker") {
			t.Fatalf("inspecting a removal must stay read-only and off docker, ran: %s", l)
		}
	}
}

func TestInspectRemovalOfACleanWorktree(t *testing.T) {
	f := newFixture(t)
	recordIndex(t, f, "feat/x", 3)
	plan, err := InspectRemoval(context.Background(), f.opts("feat/x"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.root, ".worktrees", "feat", "x")
	if plan.Kind != RemoveCreated || plan.Path != want || plan.Index != 3 || plan.RequiresForce() {
		t.Fatalf("plan = %+v, want a created worktree at %s, index 3, no --force", plan, want)
	}
	assertNothingChanged(t, f)
}

func TestInspectRemovalNamesTheTrackedChanges(t *testing.T) {
	f := newFixture(t)
	dirty(f)
	plan, err := InspectRemoval(context.Background(), f.opts("feat/x"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Changes, "backend/models.py") || !plan.RequiresForce() {
		t.Fatalf("plan = %+v, want the change listed and --force required", plan)
	}
	assertNothingChanged(t, f)
}

// A lock and uncommitted work are two reasons a screen asking for --force
// should both show: forcing past the lock throws the changes away too.
func TestInspectRemovalOfALockedWorktreeStillLooksForChanges(t *testing.T) {
	f := newFixture(t)
	f.lockReason = "claude session curry (pid 79510)"
	dirty(f)
	plan, err := InspectRemoval(context.Background(), f.opts("feat/x"))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Locked || plan.LockReason != f.lockReason || plan.Changes == "" || !plan.RequiresForce() {
		t.Fatalf("plan = %+v, want the lock, its reason and the changes", plan)
	}
}

func TestInspectRemovalUnderForceSkipsTheStatus(t *testing.T) {
	f := newFixture(t)
	dirty(f)
	o := f.opts("feat/x")
	o.Force = true
	if _, err := InspectRemoval(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := lastCall(f, "status --porcelain"); got != "" {
		t.Fatalf("--force skips the status check, ran %q", got)
	}
}

func TestInspectRemovalOfAnAdoptedWorktreeKeepsTheCheckout(t *testing.T) {
	f, path := foreignFixture(t)
	f.managed = map[string]bool{"worktree-curry": true}
	dirty(f)
	plan, err := InspectRemoval(context.Background(), f.opts("worktree-curry"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != RemoveAdopted || plan.Path != path || plan.RequiresForce() {
		t.Fatalf("plan = %+v, want an adopted worktree at %s that needs no --force", plan, path)
	}
	assertNothingChanged(t, f)
}

func TestInspectRemovalOfAVanishedWorktreeReleasesItsIndex(t *testing.T) {
	f := newFixture(t)
	recordIndex(t, f, "feat/gone", 5)
	plan, err := InspectRemoval(context.Background(), f.opts("feat/gone"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != RemoveStale || plan.Index != 5 || plan.Path != "" || plan.RequiresForce() {
		t.Fatalf("plan = %+v, want a stale index 5 with no directory", plan)
	}
	assertNothingChanged(t, f)
}

func TestInspectRemovalOfAnAbandonedDirectoryRequiresForce(t *testing.T) {
	f := newFixture(t)
	dest := filepath.Join(f.root, ".worktrees", "feat", "gone")
	mustWrite(t, filepath.Join(dest, ".git"), "gitdir: /pruned\n")
	plan, err := InspectRemoval(context.Background(), f.opts("feat/gone"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != RemoveAbandoned || plan.Path != dest || !plan.RequiresForce() {
		t.Fatalf("plan = %+v, want the abandoned directory %s, --force required", plan, dest)
	}
	assertNothingChanged(t, f)
}

func TestInspectRemovalOfAnUnknownBranchKeepsGitsAnswer(t *testing.T) {
	f := newFixture(t)
	_, err := InspectRemoval(context.Background(), f.opts("feat/never"))
	if err == nil || !strings.Contains(err.Error(), "no worktree for branch") {
		t.Fatalf("InspectRemoval = %v, want git's own listing error", err)
	}
}
