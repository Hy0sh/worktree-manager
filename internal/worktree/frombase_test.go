package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// An existing branch is checked out as-is and the base is ignored, so the flag
// would quietly do nothing. Saying so beats a line in the log nobody reads.
func TestCreateFromHereRefusesAnExistingBranch(t *testing.T) {
	f := newFixture(t)
	f.branchHead = true
	o := f.opts("feat/x")
	o.Base = "feat/other"
	o.BaseFromHere = true
	err := Create(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "--from-here") {
		t.Fatalf("expected a refusal naming the flag, got %v", err)
	}
	if got := lastCall(f, "worktree add"); got != "" {
		t.Fatalf("nothing may be checked out, got %q", got)
	}
}

// Without the flag, an existing branch stays the ordinary case: reused as-is,
// with the base ignored and a line saying so.
func TestCreateStillReusesAnExistingBranchWithoutTheFlag(t *testing.T) {
	f := newFixture(t)
	f.branchHead = true
	if err := Create(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := lastCall(f, "worktree add"); !strings.HasSuffix(got, " feat/x") {
		t.Fatalf("the branch should be reused as-is, got %q", got)
	}
}

// The base is cut from the local ref, which a create never refreshed and which
// can be weeks old. Moving the cut to the tracking ref would silently drop
// unpushed commits, so wtm states the gap and leaves the decision.
func TestCreateSaysWhenTheBaseIsBehind(t *testing.T) {
	f := newFixture(t)
	f.remotes = []string{"origin"}
	var out strings.Builder
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		if strings.Contains(line, "refs/remotes/origin/develop") {
			return execx.Result{Stdout: "abc\n"}, nil
		}
		if strings.Contains(line, "rev-list --count") {
			return execx.Result{Stdout: "12\n"}, nil
		}
		return inner(c)
	}
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(out.String(), "develop is 12 commit(s) behind origin/develop") {
		t.Fatalf("expected the gap to be named, got:\n%s", out.String())
	}
	if got := lastCall(f, "worktree add"); !strings.HasSuffix(got, " develop") {
		t.Fatalf("the cut must stay on the local ref, got %q", got)
	}
}

func TestCreateSaysNothingWhenTheBaseIsUpToDate(t *testing.T) {
	f := newFixture(t)
	f.remotes = []string{"origin"}
	var out strings.Builder
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		if strings.Contains(line, "refs/remotes/origin/develop") {
			return execx.Result{Stdout: "abc\n"}, nil
		}
		if strings.Contains(line, "rev-list --count") {
			return execx.Result{Stdout: "0\n"}, nil
		}
		return inner(c)
	}
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if strings.Contains(out.String(), "behind") {
		t.Fatalf("nothing to say when the base is current, got:\n%s", out.String())
	}
}

// Offline, or behind a VPN, the fetch fails and the count cannot be trusted.
// A create is not the place to report a network problem.
func TestCreateStaysSilentWhenTheFetchFails(t *testing.T) {
	f := newFixture(t)
	f.remotes = []string{"origin"}
	var out strings.Builder
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		if strings.Contains(line, "refs/remotes/origin/develop") {
			return execx.Result{Stdout: "abc\n"}, nil
		}
		if strings.Contains(line, "rev-list --count") {
			return execx.Result{ExitCode: 128}, errors.New("bad revision")
		}
		return inner(c)
	}
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if strings.Contains(out.String(), "behind") {
		t.Fatalf("no gap can be claimed without a count, got:\n%s", out.String())
	}
}
