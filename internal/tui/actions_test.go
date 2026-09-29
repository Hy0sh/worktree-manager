package tui

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Hy0sh/worktree-manager/internal/stack"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

func ran(t *testing.T, s *source, want ...[]string) {
	t.Helper()
	if len(want) == 0 && len(s.ran) == 0 {
		return
	}
	if !reflect.DeepEqual(s.ran, want) {
		t.Fatalf("ran %q, want %q", s.ran, want)
	}
}

func TestStartAndStopRunTheWtmVerbs(t *testing.T) {
	s := &source{}
	m, _ := loaded(t, s, entry("feat/a", "down", 1))
	m, start := step(t, m, key("s"))
	m = back(t, m, "start")
	_, stop := step(t, m, key("x"))
	if start == nil || stop == nil {
		t.Fatal("s and x should each hand the terminal to wtm")
	}
	ran(t, s, []string{"start", "repo", "feat/a"}, []string{"stop", "repo", "feat/a"})
}

// back is the verb returning the terminal, once the replayed keys have settled.
func back(t *testing.T, m Model, verb string) Model {
	t.Helper()
	m, _ = step(t, m, actionMsg{verb: verb, branch: "feat/a"})
	m.settle = time.Time{}
	return m
}

// Keys typed at docker's output are replayed when the dashboard takes the
// terminal back: an enter meant for a slow start opened a shell.
func TestKeysTypedWhileAVerbRunsAreDropped(t *testing.T) {
	s := &source{}
	m, _ := loaded(t, s, entry("feat/a", "up", 1))
	m, _ = step(t, m, key("s"))
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, key("d"), key("y"), key("x")} {
		var cmd tea.Cmd
		if m, cmd = step(t, m, k); cmd != nil {
			t.Fatalf("%s arrived while start had the terminal and must be dropped", k)
		}
	}
	m, _ = step(t, m, actionMsg{verb: "start", branch: "feat/a"})
	if _, cmd := step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("an enter replayed right after the return must be dropped too")
	}
	m.settle = time.Time{}
	step(t, m, key("x"))
	ran(t, s, []string{"start", "repo", "feat/a"}, []string{"stop", "repo", "feat/a"})
}

func TestEnterOpensTheUserShellInTheWorktree(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	s := &source{}
	m, _ := loaded(t, s, entry("feat/a", "up", 1))
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	ran(t, s, []string{"run", "repo", "feat/a", "--", "/bin/zsh"})
}

// Following a stack that is down ends at once: the screen only blinked.
func TestLogsWaitForAnUpStack(t *testing.T) {
	s := &source{}
	m, _ := loaded(t, s, entry("feat/b", "down", 0), entry("feat/c", "down", 3), entry("feat/a", "up", 1))
	for range 2 {
		m, _ = step(t, m, key("l"))
		ran(t, s)
		if !strings.Contains(m.note, "s starts it") {
			t.Fatalf("note = %q, want the way to an up stack", m.note)
		}
		m, _ = step(t, m, key("j"))
	}
	step(t, m, key("l"))
	ran(t, s, []string{"logs", "repo", "feat/a"})
}

func TestNothingRunsOnAWorktreeLeftToAdopt(t *testing.T) {
	s := &source{}
	curry := worktree.Entry{Worktree: stack.Worktree{Branch: "worktree-curry",
		Path: "/repo/.claude/worktrees/curry"}, Status: worktree.StatusAdoptable}
	m, _ := loaded(t, s, curry)
	for _, k := range []tea.KeyPressMsg{key("s"), key("x"), key("d"), key("l"), {Code: tea.KeyEnter}} {
		var cmd tea.Cmd
		if m, cmd = step(t, m, k); cmd != nil {
			t.Fatalf("%s should do nothing on an adoptable worktree", k)
		}
	}
	ran(t, s)
	if !strings.Contains(m.note, "wtm adopt worktree-curry") {
		t.Fatalf("note = %q, want the adopt line", m.note)
	}
}

func confirming(t *testing.T, s *source, e worktree.Entry) Model {
	t.Helper()
	m, _ := loaded(t, s, e)
	m, cmd := step(t, m, key("d"))
	if cmd == nil {
		t.Fatal("d should inspect the removal first")
	}
	m, _ = step(t, m, cmd())
	if m.confirm == nil {
		t.Fatal("the plan should be waiting for an answer")
	}
	ran(t, s)
	return m
}

// --force is the plan's to ask for: forcing a clean removal would also force
// past a lock taken between the question and the answer.
func TestRemoveAsksFirstAndForcesOnlyWhenThePlanSaysSo(t *testing.T) {
	s := &source{plan: worktree.RemovalPlan{Kind: worktree.RemoveCreated,
		Changes: " M backend/models.py"}}
	m := confirming(t, s, entry("feat/a", "up", 1))
	view := m.render()
	for _, want := range []string{"remove feat/a?", "stack is up", "backend/models.py", "y remove with --force"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the question should say %q:\n%s", want, view)
		}
	}
	step(t, m, key("y"))
	ran(t, s, []string{"remove", "--force", "repo", "feat/a"})

	s = &source{plan: worktree.RemovalPlan{Kind: worktree.RemoveAdopted}}
	m = confirming(t, s, entry("feat/a", "down", 1))
	if view := m.render(); !strings.Contains(view, "the checkout stays") || strings.Contains(view, "--force") {
		t.Fatalf("an adopted worktree keeps its checkout and needs no force:\n%s", view)
	}
	step(t, m, key("y"))
	ran(t, s, []string{"remove", "repo", "feat/a"})
}

func TestAPendingRemovalListensToItsAnswerAlone(t *testing.T) {
	s := &source{plan: worktree.RemovalPlan{Kind: worktree.RemoveCreated}}
	m := confirming(t, s, entry("feat/a", "down", 1))
	m, _ = step(t, m, key("s"))
	if m.confirm == nil {
		t.Fatal("a stray key must leave the question standing")
	}
	m, cmd := step(t, m, key("q"))
	if cmd != nil || m.confirm != nil {
		t.Fatal("q answers no here, it does not quit")
	}
	ran(t, s)
	if m.note != "nothing removed" {
		t.Fatalf("note = %q", m.note)
	}
}

// The listing running when an action ends may have started before it, and
// would show the worktree as it was: one more has to follow.
func TestAnActionIsFollowedByAFreshListing(t *testing.T) {
	s := &source{}
	m, _ := loaded(t, s, entry("feat/a", "down", 0))
	m, cmd := step(t, m, actionMsg{verb: "start", branch: "feat/a"})
	if cmd == nil || !m.loading {
		t.Fatal("an action should list again at once")
	}

	m, cmd = step(t, m, actionMsg{verb: "stop", branch: "feat/a"})
	if cmd != nil || !m.again {
		t.Fatal("with a listing running, the next one waits for it")
	}
	m, cmd = step(t, m, snapshotMsg{entries: []worktree.Entry{entry("feat/a", "up", 0)}})
	if cmd == nil {
		t.Fatal("the answer of the older listing should call for another")
	}
	cmd()
	if s.lists != 1 || m.again {
		t.Fatalf("lists = %d, again = %v", s.lists, m.again)
	}
}

func interruptedErr(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "kill -INT $$").Run()
	if !interrupted(err) {
		t.Fatalf("%v should read as interrupted", err)
	}
	return err
}

func TestTheNoteSaysHowAnActionEnded(t *testing.T) {
	failed := exec.Command("sh", "-c", "exit 1").Run()
	for _, c := range []struct {
		msg    actionMsg
		note   string
		failed bool
	}{
		{actionMsg{verb: "start", branch: "feat/a"}, "start feat/a: done", false},
		{actionMsg{verb: "remove", branch: "feat/a", err: failed}, "remove feat/a failed: exit status 1", true},
		{actionMsg{verb: "start", branch: "feat/a", err: interruptedErr(t)}, "start feat/a interrupted", true},
		{actionMsg{verb: "shell", branch: "feat/a", err: failed}, "", false},
		{actionMsg{verb: "logs", branch: "feat/a", err: interruptedErr(t)}, "", false},
		{actionMsg{verb: "logs", branch: "feat/a", err: failed}, "logs feat/a failed: exit status 1", true},
	} {
		m := New(context.Background(), "repo", (&source{}).Source())
		m.done(c.msg)
		if m.note != c.note || m.failed != c.failed {
			t.Fatalf("%s %v: note %q (failed %v), want %q (%v)",
				c.msg.verb, c.msg.err, m.note, m.failed, c.note, c.failed)
		}
	}
}

func TestAFailureWaitsForEnterBeforeTheDashboardComesBack(t *testing.T) {
	for _, c := range []struct {
		name   string
		script string
		pause  bool
		waits  bool
	}{
		{"failure", "exit 3", true, true},
		{"success", "exit 0", true, false},
		{"ctrl+c", "kill -INT $$", true, false},
		{"shell", "exit 3", false, false},
	} {
		var stdout, stderr bytes.Buffer
		h := &held{cmd: exec.Command("sh", "-c", c.script), what: "start feat/a",
			banner: "wtm start feat/a" + comesBack, pause: c.pause}
		h.SetStdin(strings.NewReader("\n"))
		h.SetStdout(&stdout)
		h.SetStderr(&stderr)
		err := h.Run()
		if (err != nil) != (c.script != "exit 0") {
			t.Fatalf("%s: Run = %v", c.name, err)
		}
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatalf("%s: the command's own error should come back, got %v", c.name, err)
		}
		if !strings.HasPrefix(stdout.String(), "\x1b[H\x1b[2J") || !strings.Contains(stdout.String(), "comes back") {
			t.Fatalf("%s: the screen should be cleared and headed first, got %q", c.name, stdout.String())
		}
		if waits := strings.Contains(stderr.String(), "press enter"); waits != c.waits {
			t.Fatalf("%s: waited = %v, want %v (%q)", c.name, waits, c.waits, stderr.String())
		}
	}
}

func TestTheKeyLineOffersWhatTheSelectedRowTakes(t *testing.T) {
	curry := worktree.Entry{Worktree: stack.Worktree{Branch: "worktree-curry",
		Path: "/repo/.claude/worktrees/curry"}, Status: worktree.StatusAdoptable}
	for _, c := range []struct {
		name    string
		entries []worktree.Entry
		want    string
		not     string
	}{
		{"empty", nil, "r refresh · q quit", "start"},
		{"adoptable", []worktree.Entry{curry}, "↑/↓ move · r refresh", "start"},
		{"no index", []worktree.Entry{entry("feat/b", "down", 0)}, "s start", "l logs"},
		{"down", []worktree.Entry{entry("feat/c", "down", 3)}, "x stop", "l logs"},
		{"indexed", []worktree.Entry{entry("feat/a", "up", 1)}, "l logs", ""},
	} {
		m, _ := loaded(t, &source{}, c.entries...)
		view := m.render()
		if !strings.Contains(view, c.want) || c.not != "" && strings.Contains(view, c.not) {
			t.Fatalf("%s: want %q and not %q in:\n%s", c.name, c.want, c.not, view)
		}
	}
}
