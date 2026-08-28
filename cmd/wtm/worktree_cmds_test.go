package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// allFixture registers a project holding three worktrees, each with a recorded
// index, which is what `stop` and `remove` need to name a compose project. The
// middle one is what makes the fixture worth three: a failure there must leave
// the first and the last handled all the same.
type allFixture struct {
	app  *app
	out  *bytes.Buffer
	fake *execx.Fake
	dir  string
}

func newAllFixture(t *testing.T, in string) *allFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &config.Config{Projects: map[string]config.Project{
		"myapp": {Dir: dir, WorktreeIndices: map[string]int{"feat/a": 1, "feat/b": 2, "feat/c": 3}},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &allFixture{out: &bytes.Buffer{}, dir: dir}
	f.fake = &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: "worktree " + dir + "\nHEAD abc\nbranch refs/heads/main\n\n" +
				"worktree " + filepath.Join(dir, ".worktrees", "feat/a") + "\nHEAD aaa\nbranch refs/heads/feat/a\n\n" +
				"worktree " + filepath.Join(dir, ".worktrees", "feat/b") + "\nHEAD bbb\nbranch refs/heads/feat/b\n\n" +
				"worktree " + filepath.Join(dir, ".worktrees", "feat/c") + "\nHEAD ccc\nbranch refs/heads/feat/c\n"}, nil
		}
		return execx.Result{}, nil
	}}
	f.app = &app{cfg: cfg, cfgPath: cfgPath, backups: t.TempDir(),
		runner: f.fake, out: f.out, in: strings.NewReader(in)}
	return f
}

// Stopping a dozen branches one call at a time is the whole reason --all exists.
func TestStopAllTakesDownEveryWorktree(t *testing.T) {
	f := newAllFixture(t, "")
	cmd := newStopCmd(f.app)
	cmd.SetArgs([]string{"myapp", "--all"})
	cmd.SetOut(f.out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --all: %v", err)
	}
	repo := filepath.Base(f.dir)
	for _, want := range []string{"-p " + repo + "-wt-1-feat-a down",
		"-p " + repo + "-wt-2-feat-b down", "-p " + repo + "-wt-3-feat-c down"} {
		if !strings.Contains(strings.Join(f.fake.Lines(), "\n"), want) {
			t.Fatalf("want %q in:\n%s", want, strings.Join(f.fake.Lines(), "\n"))
		}
	}
}

// A cleanup that gave up on the worktree it could not handle would leave every
// stack after it running, which is the state --all was called to end.
func TestStopAllCarriesOnAfterAFailure(t *testing.T) {
	f := newAllFixture(t, "")
	repo := filepath.Base(f.dir)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), repo+"-wt-2-feat-b down") {
			return execx.Result{ExitCode: 1}, errors.New("container is locked")
		}
		return inner(c)
	}
	cmd := newStopCmd(f.app)
	cmd.SetArgs([]string{"myapp", "--all"})
	cmd.SetOut(f.out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a failed worktree must still fail the command")
	}
	lines := strings.Join(f.fake.Lines(), "\n")
	for _, want := range []string{"-p " + repo + "-wt-1-feat-a down", "-p " + repo + "-wt-3-feat-c down"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("the walk must carry on, want %q in:\n%s", want, lines)
		}
	}
	// Each worktree pours its own docker output over the terminal, so a warning
	// printed as it happened would have scrolled out of sight by the last one.
	if !strings.Contains(err.Error(), "1 of 3") || !strings.Contains(err.Error(), "feat/b: ") ||
		!strings.Contains(err.Error(), "container is locked") {
		t.Fatalf("the summary must count them, name which, and keep the cause, got %v", err)
	}
}

// Removing every worktree of a project at once is worth a look before it runs.
func TestRemoveAllAsksBeforeRemoving(t *testing.T) {
	f := newAllFixture(t, "n\n")
	cmd := newRemoveCmd(f.app)
	cmd.SetArgs([]string{"myapp", "--all"})
	cmd.SetOut(f.out)
	if err := cmd.Execute(); err == nil {
		t.Fatal("a refused confirmation must fail the command")
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree remove") {
			t.Fatalf("nothing may be removed after a no, got %q", l)
		}
	}
	if !strings.Contains(f.out.String(), "feat/a") || !strings.Contains(f.out.String(), "feat/c") {
		t.Fatalf("the confirmation must name what goes:\n%s", f.out.String())
	}
}

func TestRemoveAllYesRemovesEveryWorktree(t *testing.T) {
	f := newAllFixture(t, "")
	cmd := newRemoveCmd(f.app)
	cmd.SetArgs([]string{"myapp", "--all", "--yes"})
	cmd.SetOut(f.out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("remove --all --yes: %v", err)
	}
	lines := strings.Join(f.fake.Lines(), "\n")
	for _, want := range []string{
		filepath.Join(f.dir, ".worktrees", "feat/a"),
		filepath.Join(f.dir, ".worktrees", "feat/b"),
		filepath.Join(f.dir, ".worktrees", "feat/c"),
	} {
		if !strings.Contains(lines, "worktree remove --force "+want) {
			t.Fatalf("want %q removed in:\n%s", want, lines)
		}
	}
}

// --exec enters the application container, and --no-start leaves none running.
// Discovering that after the worktree, the fetch and the restore is a warning
// nobody wanted: the two flags are refused together, and the message names the
// flag that does work without a stack.
func TestCreateRefusesExecWithoutAStack(t *testing.T) {
	f := newAllFixture(t, "")
	cmd := newCreateCmd(f.app)
	cmd.SetArgs([]string{"myapp", "feat/x", "--exec", "manage.py seed_data", "--no-start"})
	cmd.SetOut(f.out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--exec --no-start must be refused")
	}
	if !strings.Contains(err.Error(), "--run") {
		t.Fatalf("the refusal should name the flag that works without a stack, got %v", err)
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree add") {
			t.Fatalf("nothing should have been created, got %q", l)
		}
	}
}
