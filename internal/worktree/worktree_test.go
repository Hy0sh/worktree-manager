package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

func TestCreateAbortsWhenDestinationExists(t *testing.T) {
	f := newFixture(t)
	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	o := f.opts("feat/x")
	o.NoStart = true
	err := Create(context.Background(), o)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), dest) {
		t.Fatalf("error should mention the destination, got %q", err.Error())
	}
	if len(f.fake.Calls) != 0 {
		t.Fatalf("no command should run when the destination exists, got %v", f.fake.Lines())
	}
}

func TestCreateStartsStackUnlessNoStart(t *testing.T) {
	f := newFixture(t)
	if err := Create(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var started bool
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "up -d --build") {
			started = true
		}
	}
	if !started {
		t.Fatalf("wtc start should have run, calls = %v", f.fake.Lines())
	}
}

func TestCreateNoStartSkipsWtcEntirely(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "docker") {
			t.Fatalf("--no-start must not touch docker, got %q", l)
		}
	}
}

// Stop must use the index the registry recorded, not the position git
// happens to list the worktree at, or a stack started at one index would be
// addressed at another once other worktrees come and go.
func TestStopUsesResolvedIndex(t *testing.T) {
	f := newFixture(t)
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		p := c.Projects["myapp"]
		p.WorktreeIndices = map[string]int{"feat/x": 7}
		c.Projects["myapp"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := Stop(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	last := f.fake.Lines()[len(f.fake.Lines())-1]
	want := stack.ProjectName(filepath.Base(f.root), 7, "feat/x")
	if !strings.Contains(last, "compose -p "+want+" down") {
		t.Fatalf("last call = %q", last)
	}
}

func TestStopWithoutAnyRecordedStackIsANoOp(t *testing.T) {
	f := newFixture(t)
	// The branch exists as a worktree but never started a stack and docker
	// has no trace of it — and its git position is squatted by another
	// branch's debris, so the fallback must not fire either. The label is
	// built with the real naming function because the fixture's repo name is
	// a temp directory, not a fixed string.
	f.dockerLabels = []string{stack.ProjectName(filepath.Base(f.root), 1, "someone-else")}
	// feat/x is listed at position 1 by the fixture's porcelain output.
	if err := Stop(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("stopping a stack that never existed must be a note, not an error: %v", err)
	}
	for _, line := range f.fake.Lines() {
		if strings.Contains(line, "compose") && strings.Contains(line, "down") {
			t.Fatalf("nothing should be taken down, ran: %s", line)
		}
	}
}

func TestRemoveReleasesTheIndex(t *testing.T) {
	f := newFixture(t)
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := cfg.Projects["myapp"].WorktreeIndices["feat/x"]; held {
		t.Fatal("remove must release the branch's index")
	}
}

func TestCreateRecordsTheAllocatedIndex(t *testing.T) {
	f := newFixture(t)
	f.branchHead = true
	if err := Create(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Projects["myapp"].WorktreeIndices["feat/x"]; got < 1 {
		t.Fatalf("create must persist the index it started the stack with, got %d", got)
	}
}

func TestRemoveStopsThenRemovesWorktree(t *testing.T) {
	f := newFixture(t)
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	lines := f.fake.Lines()
	var stopAt, removeAt = -1, -1
	for i, l := range lines {
		if strings.Contains(l, "down") && strings.Contains(l, "compose") {
			stopAt = i
		}
		if strings.Contains(l, "worktree remove") {
			removeAt = i
		}
	}
	if stopAt == -1 || removeAt == -1 || stopAt > removeAt {
		t.Fatalf("expected stop before remove, got %v", lines)
	}
}

// A worktree always holds untracked files the tool itself created (.env copies,
// .git-container, .db-snapshot), which plain `git worktree remove` refuses to
// delete. Only tracked changes are worth protecting.
func TestRemoveForcesWhenOnlyUntrackedArtifactsRemain(t *testing.T) {
	f := newFixture(t)
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var removeLine string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree remove") {
			removeLine = l
		}
	}
	if !strings.Contains(removeLine, "--force") {
		t.Fatalf("removal must force past the tool's own untracked files, got %q", removeLine)
	}
}

func TestRemoveRefusesWhenTrackedFilesAreModified(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "status --porcelain") {
			return execx.Result{Stdout: " M backend/models.py\n"}, nil
		}
		return inner(c)
	}
	err := Remove(context.Background(), f.opts("feat/x"))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "backend/models.py") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error should list the change and mention --force, got %q", err.Error())
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree remove") {
			t.Fatal("nothing should be removed when tracked files are modified")
		}
	}
	// A refusal has to leave the worktree as it was: taking the stack down and
	// then declining is the worst of both, and the developer's dev server is
	// gone for a removal that never happened.
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "down") {
			t.Fatalf("the stack must stay up when the removal is refused, ran: %s", l)
		}
	}
}

func TestRemoveWithForceIgnoresTrackedChanges(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "status --porcelain") {
			return execx.Result{Stdout: " M backend/models.py\n"}, nil
		}
		return inner(c)
	}
	o := f.opts("feat/x")
	o.Force = true
	if err := Remove(context.Background(), o); err != nil {
		t.Fatalf("Remove --force: %v", err)
	}
	var removed bool
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree remove") && strings.Contains(l, "--force") {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("--force must go through, calls = %v", f.fake.Lines())
	}
}

func TestRemovePropagatesGitFailure(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree remove") {
			return execx.Result{}, errors.New("contains modified or untracked files")
		}
		return inner(c)
	}
	err := Remove(context.Background(), f.opts("feat/x"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "modified or untracked") {
		t.Fatalf("git error should be propagated, got %q", err.Error())
	}
}

// Stopping a worktree used to be a one-way trip: bringing it back meant
// calling wtc with the index it derives.
func TestStartBringsAnExistingWorktreeBackUp(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), "services:\n  db: {}\n")
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}

	f.fake.Calls = nil
	if err := Start(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var started bool
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "up -d --build") {
			started = true
		}
	}
	if !started {
		t.Fatalf("wtc start should have run, calls = %v", f.fake.Lines())
	}
}

// Plenty of repositories have no docker stack at all. The worktree is still
// useful there, so this must be a note and not a failure.
func TestCreateOnAProjectWithoutCompose(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.root, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("a project without compose must not fail: %v", err)
	}
	if !strings.Contains(out.String(), "no compose file") {
		t.Fatalf("the absence of a stack should be stated:\n%s", out.String())
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "docker") {
			t.Fatalf("nothing docker should run, got %q", l)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees", "feat", "x")); err != nil {
		t.Fatalf("the worktree itself must still be created: %v", err)
	}
}

func TestStopAndRemoveWithoutCompose(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.root, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := Stop(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// A branch name is not a safe path fragment, and git only rejects such a
// refname after directories have been created for it.
func TestCreateRefusesABranchEscapingTheWorktreeDirectory(t *testing.T) {
	f := newFixture(t)
	for _, branch := range []string{"../../evil", "..", "../outside"} {
		o := f.opts(branch)
		o.NoStart = true
		err := Create(context.Background(), o)
		if err == nil {
			t.Fatalf("branch %q should be refused", branch)
		}
		if !strings.Contains(err.Error(), "invalid branch name") {
			t.Fatalf("error for %q = %v", branch, err)
		}
		if len(f.fake.Calls) != 0 {
			t.Fatalf("nothing should run for %q, got %v", branch, f.fake.Lines())
		}
	}
	// The escape target must not have been created either.
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.root), "evil")); !os.IsNotExist(err) {
		t.Fatal("a directory was created outside the project")
	}
}
