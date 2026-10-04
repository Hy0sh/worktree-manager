package worktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// driftedFixture is an adopted worktree whose stack sits at index 2 under
// feat/old, switched to feat/next by a bare `git switch` since.
func driftedFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "curry")
	f.foreign = map[string]string{"feat/next": path}
	f.managed = map[string]bool{"feat/old": true}
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		p := c.Projects["myapp"]
		p.WorktreeIndices = map[string]int{"feat/old": 2}
		p.WorktreePaths = map[string]string{"feat/old": path}
		c.Projects["myapp"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, path
}

func (f *fixture) driftedOpts(branch, path string) Options {
	o := f.opts(branch)
	o.Stack.Paths = map[string]string{"feat/old": path}
	return o
}

// git lists the worktree under feat/next alone, so feat/old used to be "no
// worktree" for every verb, while its stack ran on, out of everybody's reach.
func TestADriftedStackIsAddressedByItsRecordedBranch(t *testing.T) {
	f, path := driftedFixture(t)
	o := f.driftedOpts("feat/old", path)
	wt, err := o.Stack.FindByBranch(context.Background(), "feat/old")
	if err != nil {
		t.Fatalf("FindByBranch: %v", err)
	}
	if wt.Path != path || wt.Branch != "feat/old" || wt.Holds != "feat/next" {
		t.Fatalf("got %+v", wt)
	}
	if err := Stop(context.Background(), o); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := "compose -p " + stack.ProjectName(filepath.Base(f.root), 2, "feat/old") + " stop"
	if lastCall(f, want) == "" {
		t.Fatalf("want %q in %v", want, f.fake.Lines())
	}
}

// Removing it takes the stack and wtm's files, never the checkout: that one now
// holds another branch's work.
func TestRemovingADriftedStackKeepsTheCheckout(t *testing.T) {
	f, path := driftedFixture(t)
	if err := Remove(context.Background(), f.driftedOpts("feat/old", path)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if lastCall(f, "down --volumes") == "" {
		t.Fatalf("the stack should go, ran %v", f.fake.Lines())
	}
	if lastCall(f, "worktree remove") != "" {
		t.Fatal("the checkout must stay")
	}
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := cfg.Projects["myapp"].WorktreeIndices["feat/old"]; n != 0 {
		t.Fatalf("the index should be released, still %d", n)
	}
}

// Adopting it again would give the directory a second stack and orphan the
// first, which `wtm list` used to invite by counting it as left to adopt.
func TestADriftedWorktreeIsNeitherAdoptableNorListedAsSuch(t *testing.T) {
	f, path := driftedFixture(t)
	err := Adopt(context.Background(), f.driftedOpts("feat/next", path))
	if err == nil || !strings.Contains(err.Error(), "already has a stack") {
		t.Fatalf("adopt should refuse, got %v", err)
	}
	entries, err := List(context.Background(), f.driftedOpts("", path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Path != path {
			continue
		}
		if e.Adoptable() || e.Branch != "feat/old" || e.BranchLabel() != "feat/old (now on feat/next)" {
			t.Fatalf("got %+v, label %q", e, e.BranchLabel())
		}
		return
	}
	t.Fatalf("the worktree is not listed: %+v", entries)
}

// Switching branches inside a worktree takes its old name out of `git worktree
// list` while its containers keep carrying it, so the branch reads as vanished.
// releaseStale runs on every create: taking that stack down with --volumes
// would delete the database of a worktree somebody is working in.
func TestAStaleIndexWhoseStackStillRunsIsNotTornDown(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps -q") {
			return execx.Result{Stdout: "abc123\ndef456\n"}, nil
		}
		return inner(c)
	}
	o := f.opts("feat/gone")
	o.Inferred = true // as a create's own sweep runs it
	err := releaseStale(context.Background(), o, 3)
	if err == nil {
		t.Fatal("a running stack must not be taken down as a leftover")
	}
	if !strings.Contains(err.Error(), "switched branches") {
		t.Fatalf("the error should name what this looks like, got %q", err)
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "down") || strings.Contains(l, "rmi") || strings.Contains(l, "volume rm") {
			t.Fatalf("nothing may be removed: %q", l)
		}
	}
}

// `wtm clean` exists to sweep a worktree removed outside wtm, and it may well
// have been removed while its stack was up. Somebody asked for that one, so it
// goes: the guard above covers the sweep nobody asked for, and only that.
func TestAnAskedForRemovalSweepsARunningStack(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps -q") {
			return execx.Result{Stdout: "abc123\n"}, nil
		}
		return inner(c)
	}
	o := f.opts("feat/gone") // Inferred left false: this one was typed
	if err := releaseStale(context.Background(), o, 3); err != nil {
		t.Fatalf("releaseStale: %v", err)
	}
	var down bool
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "down") && strings.Contains(l, "--volumes") {
			down = true
		}
	}
	if !down {
		t.Fatalf("an asked-for removal must still sweep, ran %v", f.fake.Lines())
	}
}

// The sweep still has to work: a worktree removed outside wtm leaves stopped
// containers, and that stack is wtm's to take down.
func TestAStaleIndexWithNothingRunningIsStillSwept(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps -q") {
			return execx.Result{}, nil // nothing running
		}
		return inner(c)
	}
	o := f.opts("feat/gone")
	if err := releaseStale(context.Background(), o, 3); err != nil {
		t.Fatalf("releaseStale: %v", err)
	}
	var down bool
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "down") && strings.Contains(l, "--volumes") {
			down = true
		}
	}
	if !down {
		t.Fatalf("the leftover stack should have been taken down, ran %v", f.fake.Lines())
	}
}
