package worktree

import (
	"context"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

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
