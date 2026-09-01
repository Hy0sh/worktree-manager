package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// foreignFixture stands in a worktree `claude -w` created, which git lists
// outside .worktrees and every wtm command hides until it is adopted.
func foreignFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	path := filepath.Join(f.root, ".claude", "worktrees", "curry")
	f.foreign = map[string]string{"worktree-curry": path}
	f.cwd = "worktree-curry"
	return f, path
}

func TestAdoptTakesTheWorktreeOfTheCurrentDirectory(t *testing.T) {
	f, path := foreignFixture(t)
	o := f.opts("")
	if err := Adopt(context.Background(), o); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := lastCall(f, "compose"); !strings.Contains(got, path) {
		t.Fatalf("the stack must come up on the worktree itself, got %q", got)
	}
	if got := lastCall(f, "worktree add"); got != "" {
		t.Fatalf("adopting checks nothing out, got %q", got)
	}
}

// The index is what every other command reads to see the worktree at all, so
// adopting has to record one.
func TestAdoptRecordsAnIndex(t *testing.T) {
	f, _ := foreignFixture(t)
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Projects["myapp"].WorktreeIndices["worktree-curry"] == 0 {
		t.Fatalf("no index recorded, got %+v", cfg.Projects["myapp"].WorktreeIndices)
	}
}

func TestAdoptTakesANamedBranch(t *testing.T) {
	f, path := foreignFixture(t)
	f.cwd = "" // typed from the main repository, naming the branch instead
	if err := Adopt(context.Background(), f.opts("worktree-curry")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := lastCall(f, "compose"); !strings.Contains(got, path) {
		t.Fatalf("the stack must come up on the named worktree, got %q", got)
	}
}

func TestAdoptRefusesTheMainRepository(t *testing.T) {
	f, _ := foreignFixture(t)
	f.cwd = ""
	err := Adopt(context.Background(), f.opts(""))
	if err == nil || !strings.Contains(err.Error(), "repository itself") {
		t.Fatalf("expected a refusal naming the repository, got %v", err)
	}
}

func TestAdoptRefusesADetachedHead(t *testing.T) {
	f, _ := foreignFixture(t)
	f.cwdDetached = true
	err := Adopt(context.Background(), f.opts(""))
	if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("expected a refusal naming the detached HEAD, got %v", err)
	}
}

func TestAdoptRefusesAWorktreeWtmCreated(t *testing.T) {
	f := newFixture(t)
	err := Adopt(context.Background(), f.opts("feat/x"))
	if err == nil || !strings.Contains(err.Error(), "wtm start feat/x") {
		t.Fatalf("expected a refusal pointing at start, got %v", err)
	}
}

func TestAdoptRefusesTwice(t *testing.T) {
	f, _ := foreignFixture(t)
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("first Adopt: %v", err)
	}
	f.managed = map[string]bool{"worktree-curry": true}
	err := Adopt(context.Background(), f.opts(""))
	if err == nil || !strings.Contains(err.Error(), "already adopted") {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

func TestAdoptAsksBeforeWriting(t *testing.T) {
	f, path := foreignFixture(t)
	var asked string
	o := f.opts("")
	o.ConfirmAdopt = func(q string) bool { asked = q; return false }
	err := Adopt(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("a declined question must stop the adoption, got %v", err)
	}
	if !strings.Contains(asked, path) {
		t.Fatalf("the question must name the directory written into, got %q", asked)
	}
	if got := lastCall(f, "compose"); got != "" {
		t.Fatalf("nothing may run after a refusal, got %q", got)
	}
}

// A .env the worktree already carries is somebody's edit, and adopting is not
// an occasion to overwrite it.
func TestAdoptKeepsTheWorktreeOwnEnv(t *testing.T) {
	f, path := foreignFixture(t)
	mustWrite(t, filepath.Join(f.root, ".env"), "FROM_REPO=1\n")
	mustWrite(t, filepath.Join(path, ".env"), "MINE=1\n")
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	body := mustRead(t, filepath.Join(path, ".env"))
	if !strings.Contains(body, "MINE=1") {
		t.Fatalf(".env was clobbered: %q", body)
	}
}

// wtm removes what it built and nothing else: the checkout of an adopted
// worktree came from somewhere else, and something may still be working in it.
func TestRemoveKeepsAnAdoptedWorktree(t *testing.T) {
	f, path := foreignFixture(t)
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	f.managed = map[string]bool{"worktree-curry": true}
	if err := Remove(context.Background(), f.opts("worktree-curry")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := lastCall(f, "worktree remove"); got != "" {
		t.Fatalf("the directory must be left alone, got %q", got)
	}
	var down bool
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "compose") && strings.Contains(l, "down") {
			down = true
		}
	}
	if !down {
		t.Fatalf("the stack must still come down, ran %v", f.fake.Lines())
	}
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Projects["myapp"].WorktreeIndices["worktree-curry"]; ok {
		t.Fatal("the index must be released, or the worktree stays visible with no stack")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the worktree directory is gone: %v", err)
	}
}

// A worktree somebody works in always holds uncommitted work. Refusing over it
// would make the command unusable, and nothing is deleted to protect it from.
func TestRemoveOfAnAdoptedWorktreeIgnoresUncommittedChanges(t *testing.T) {
	f, _ := foreignFixture(t)
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	f.managed = map[string]bool{"worktree-curry": true}
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "status --porcelain") {
			return execx.Result{Stdout: " M backend/models.py\n"}, nil
		}
		return inner(c)
	}
	if err := Remove(context.Background(), f.opts("worktree-curry")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// Removing takes wtm's own files back out of a checkout it never owned. Not to
// unblock anything, git worktree remove already tolerates them since they are
// in info/exclude, but a dead port block pointing at ports nothing listens on
// is not a state to leave someone in.
func TestRemoveTakesWtmFilesOutOfAnAdoptedWorktree(t *testing.T) {
	f, path := foreignFixture(t)
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	mustWrite(t, filepath.Join(path, ".wtm-snapshot.yaml"), "services: {}\n")
	before := mustRead(t, filepath.Join(path, ".env"))
	if !strings.Contains(before, "wtc port overrides") {
		t.Fatalf("the fixture should have written a port block, got %q", before)
	}

	f.managed = map[string]bool{"worktree-curry": true}
	if err := Remove(context.Background(), f.opts("worktree-curry")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if body := mustRead(t, filepath.Join(path, ".env")); strings.Contains(body, "wtc port overrides") {
		t.Fatalf("the port block should be gone, got %q", body)
	}
	for _, name := range []string{".wtm-ports.yaml", ".wtm-snapshot.yaml", ".db-snapshot"} {
		if _, err := os.Lstat(filepath.Join(path, name)); err == nil {
			t.Fatalf("%s should have been removed", name)
		}
	}
}

// What wtm copied cannot be told from what the worktree already had, so it
// stays. Only the lines wtm can prove are its own go.
func TestRemoveLeavesCopiedFilesInAnAdoptedWorktree(t *testing.T) {
	f, path := foreignFixture(t)
	if err := Adopt(context.Background(), f.opts("")); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	mustWrite(t, filepath.Join(path, ".env"), "MINE=1\n"+mustRead(t, filepath.Join(path, ".env")))
	f.managed = map[string]bool{"worktree-curry": true}
	if err := Remove(context.Background(), f.opts("worktree-curry")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if body := mustRead(t, filepath.Join(path, ".env")); !strings.Contains(body, "MINE=1") {
		t.Fatalf("the rest of the .env must survive, got %q", body)
	}
}

// `claude -w` names its branches for itself, and that name becomes the handle
// of every later wtm command. Adopting is the only moment a rename is free:
// the compose project name carries the branch, so doing it afterwards would
// orphan the stack it names.
func TestAdoptRenamesTheBranch(t *testing.T) {
	f, path := foreignFixture(t)
	o := f.opts("")
	o.RenameTo = "feat/mine"
	if err := Adopt(context.Background(), o); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := lastCall(f, "branch -m"); !strings.Contains(got, "worktree-curry feat/mine") {
		t.Fatalf("expected a rename, got %q", got)
	}
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	indices := cfg.Projects["myapp"].WorktreeIndices
	if indices["feat/mine"] == 0 || indices["worktree-curry"] != 0 {
		t.Fatalf("the index belongs to the new name alone, got %+v", indices)
	}
	if got := lastCall(f, "compose"); !strings.Contains(got, "feat-mine") {
		t.Fatalf("the compose project must carry the new name, got %q", got)
	}
	if got := lastCall(f, "compose"); !strings.Contains(got, path) {
		t.Fatalf("the worktree itself does not move, got %q", got)
	}
}

func TestAdoptRefusesToRenameOntoAnExistingBranch(t *testing.T) {
	f, _ := foreignFixture(t)
	f.branchHead = true // every refs/heads/... resolves, so the name is taken
	o := f.opts("")
	o.RenameTo = "feat/mine"
	err := Adopt(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "feat/mine") {
		t.Fatalf("expected a refusal naming the branch, got %v", err)
	}
	if got := lastCall(f, "branch -m"); got != "" {
		t.Fatalf("nothing may be renamed, got %q", got)
	}
}

// The question is what somebody reads before saying yes, so it has to carry the
// rename: adopting under another name is not the same act.
func TestAdoptQuestionNamesTheRename(t *testing.T) {
	f, _ := foreignFixture(t)
	var asked string
	o := f.opts("")
	o.RenameTo = "feat/mine"
	o.ConfirmAdopt = func(q string) bool { asked = q; return false }
	if err := Adopt(context.Background(), o); err == nil {
		t.Fatal("expected the refusal to stop it")
	}
	if !strings.Contains(asked, "feat/mine") {
		t.Fatalf("the question must name what the branch becomes, got %q", asked)
	}
}
