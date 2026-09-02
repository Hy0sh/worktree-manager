package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

func stackWorktree(branch string, underRoot bool) stack.Worktree {
	return stack.Worktree{Branch: branch, UnderRoot: underRoot}
}

// adoptFixture holds a project whose git lists one worktree wtm created and one
// it did not, the `claude -w` kind adopt exists for.
func adoptFixture(t *testing.T, in string) (*app, *execx.Fake, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	foreign := filepath.Join(dir, ".claude", "worktrees", "curry")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  db:\n    ports:\n      - \"${DB_PORT:-5432}:5432\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Projects: map[string]config.Project{
		"myapp": {Dir: dir, WorktreeIndices: map[string]int{"feat/a": 1}},
	}}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	fake := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: "worktree " + dir + "\nHEAD abc\nbranch refs/heads/main\n\n" +
				"worktree " + filepath.Join(dir, ".worktrees", "feat/a") + "\nHEAD aaa\nbranch refs/heads/feat/a\n\n" +
				"worktree " + foreign + "\nHEAD ccc\nbranch refs/heads/worktree-curry\n"}, nil
		}
		return execx.Result{}, nil
	}}
	a := &app{cfg: cfg, cfgPath: cfgPath,
		backups: t.TempDir(), runner: fake, out: out, in: strings.NewReader(in)}
	return a, fake, out, foreign
}

// A closed input answers no, which is right for a person and wrong for a
// script, so nothing is written and the way out is named.
func TestAdoptRefusesWithNobodyToAnswer(t *testing.T) {
	a, fake, _, _ := adoptFixture(t, "")
	cmd := newAdoptCmd(a)
	cmd.SetArgs([]string{"myapp", "worktree-curry"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "-y") {
		t.Fatalf("expected a refusal naming -y, got %v", err)
	}
	for _, l := range fake.Lines() {
		if strings.Contains(l, "compose") {
			t.Fatalf("nothing may run before the question is answered, ran %q", l)
		}
	}
}

func TestAdoptYesAnswersForAScript(t *testing.T) {
	a, fake, _, foreign := adoptFixture(t, "")
	cmd := newAdoptCmd(a)
	cmd.SetArgs([]string{"myapp", "worktree-curry", "-y"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("adopt -y: %v", err)
	}
	var up bool
	for _, l := range fake.Lines() {
		if strings.Contains(l, "compose") && strings.Contains(l, foreign) {
			up = true
		}
	}
	if !up {
		t.Fatalf("the stack must come up on the adopted worktree, ran %v", fake.Lines())
	}
}

// --ignore-memory answers the memory advisory alone. Letting it also wave
// through a write into somebody's checkout would be a second decision nobody
// asked for.
func TestIgnoreMemoryDoesNotAnswerTheAdoptQuestion(t *testing.T) {
	a, _, _, _ := adoptFixture(t, "")
	cmd := newAdoptCmd(a)
	cmd.SetArgs([]string{"myapp", "worktree-curry", "--ignore-memory"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected the adoption to still be refused, got %v", err)
	}
}

func TestCompleteAdoptableOffersOnlyWhatIsNotManagedYet(t *testing.T) {
	a, _, _, _ := adoptFixture(t, "")
	got, _ := a.completeAdoptable(nil, []string{"myapp"}, "")
	if len(got) != 1 || got[0] != "worktree-curry" {
		t.Fatalf("only the unmanaged foreign worktree is adoptable, got %v", got)
	}
}

func TestRemoveAllQuestionCountsAdoptedApart(t *testing.T) {
	entries := []worktree.Entry{
		{Worktree: stackWorktree("feat/a", true)},
		{Worktree: stackWorktree("feat/b", true)},
		{Worktree: stackWorktree("worktree-curry", false)},
	}
	q := removeAllQuestion(entries)
	if !strings.Contains(q, "2 worktree(s)") || !strings.Contains(q, "1 adopted") {
		t.Fatalf("the question must count the two natures apart, got %q", q)
	}
	if !strings.Contains(q, "adopted directories kept") {
		t.Fatalf("it must say the adopted directories survive, got %q", q)
	}
	plain := removeAllQuestion(entries[:2])
	if strings.Contains(plain, "adopted") {
		t.Fatalf("nothing about adoption when there is none, got %q", plain)
	}
}

// The same refusal create gives: an --exec discovered stackless at the very end
// is a warning nobody wanted.
func TestAdoptRefusesExecWithoutAStack(t *testing.T) {
	a, _, _, _ := adoptFixture(t, "")
	cmd := newAdoptCmd(a)
	cmd.SetArgs([]string{"myapp", "worktree-curry", "-y", "--no-start", "--exec", "seed"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--exec needs the stack --no-start leaves down") {
		t.Fatalf("expected create's own refusal, got %v", err)
	}
}

// The flags create and adopt share are declared once, so the two verbs cannot
// drift apart.
func TestAdoptOffersTheSameAfterFlagsAsCreate(t *testing.T) {
	a, _, _, _ := adoptFixture(t, "")
	adopt, create := newAdoptCmd(a), newCreateCmd(a)
	for _, name := range []string{"no-start", "no-post-create", "run", "exec", "ignore-memory"} {
		af, cf := adopt.Flags().Lookup(name), create.Flags().Lookup(name)
		if af == nil {
			t.Fatalf("adopt is missing --%s", name)
		}
		if af.Usage != cf.Usage {
			t.Fatalf("--%s reads differently on the two verbs:\n  adopt:  %s\n  create: %s", name, af.Usage, cf.Usage)
		}
	}
}
