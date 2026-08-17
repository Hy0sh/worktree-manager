package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateNewBranchRunsWorktreeAddWithBase(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var addLine string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree add") {
			addLine = l
		}
	}
	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	if !strings.Contains(addLine, "-b feat/x "+dest+" develop") {
		t.Fatalf("worktree add line = %q", addLine)
	}
}

func TestCreateExistingBranchIgnoresBase(t *testing.T) {
	f := newFixture(t)
	f.branchHead = true
	var out strings.Builder
	o := f.opts("feat/x")
	o.NoStart = true
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var addLine string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree add") {
			addLine = l
		}
	}
	if strings.Contains(addLine, "-b") || strings.Contains(addLine, "develop") {
		t.Fatalf("existing branch must be reused as-is, got %q", addLine)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Fatalf("an info message about the existing branch was expected, got %q", out.String())
	}
}

// A branch that exists only on the remote used to be cut from base: same name,
// none of its commits, no upstream, and a divergence at the first push.
func TestCreateTracksABranchThatOnlyExistsOnARemote(t *testing.T) {
	f := newFixture(t)
	f.remotes = []string{"origin"}
	f.pushedOn = []string{"origin"}
	var out strings.Builder
	o := f.opts("feat/x")
	o.NoStart = true
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	addLine := lastCall(f, "worktree add")
	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	if !strings.Contains(addLine, "--track -b feat/x "+dest+" origin/feat/x") {
		t.Fatalf("worktree add line = %q", addLine)
	}
	if strings.Contains(addLine, "develop") {
		t.Fatalf("base must be ignored when the remote branch wins, got %q", addLine)
	}
	if !strings.Contains(out.String(), "upstream") {
		t.Fatalf("the reuse of the remote branch should be stated, got %q", out.String())
	}
}

// A branch pushed since the last fetch has no tracking ref yet, so only a fetch
// can tell it apart from a branch that exists nowhere.
func TestCreateFetchesBeforeConcludingTheBranchIsNew(t *testing.T) {
	f := newFixture(t)
	f.remotes = []string{"origin"}
	f.pushedOn = []string{"origin"}
	f.needsFetch = true
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fetch := lastCall(f, "fetch --quiet"); !strings.HasSuffix(fetch, "fetch --quiet origin feat/x") {
		t.Fatalf("fetch call = %q", fetch)
	}
	if addLine := lastCall(f, "worktree add"); !strings.Contains(addLine, "--track -b feat/x") {
		t.Fatalf("the freshly fetched branch should be tracked, got %q", addLine)
	}
}

func TestCreateCutsFromBaseWhenNoRemoteHasTheBranch(t *testing.T) {
	f := newFixture(t)
	f.remotes = []string{"origin"}
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	if addLine := lastCall(f, "worktree add"); !strings.HasSuffix(addLine, "-b feat/x "+dest+" develop") {
		t.Fatalf("worktree add line = %q", addLine)
	}
}

// git refuses to guess in the same situation, and so does wtm: two remotes
// carrying feat/x are two different branches.
func TestCreateRefusesAnAmbiguousRemoteBranch(t *testing.T) {
	f := newFixture(t)
	f.remotes = []string{"origin", "upstream"}
	f.pushedOn = []string{"origin", "upstream"}
	o := f.opts("feat/x")
	o.NoStart = true
	err := Create(context.Background(), o)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"origin", "upstream", "git branch feat/x"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}
	if line := lastCall(f, "worktree add"); line != "" {
		t.Fatalf("no worktree should be added, got %q", line)
	}
}

// A branch like feat/x nests the worktree under .worktrees/feat; git leaves
// that directory behind empty once the worktree is gone.
func TestRemovePrunesEmptyParentDirectories(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees", "feat")); !os.IsNotExist(err) {
		t.Fatal(".worktrees/feat should not be left behind empty")
	}
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees")); err != nil {
		t.Fatal(".worktrees itself must survive")
	}
}
