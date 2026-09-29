package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Hy0sh/worktree-manager/internal/stack"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

func entry(branch, status string, index int) worktree.Entry {
	e := worktree.Entry{Worktree: stack.Worktree{Index: index, Branch: branch,
		Path: "/repo/.worktrees/" + branch, UnderRoot: true}, Status: status}
	if index > 0 {
		e.ComposeProject = "repo-wt-" + branch
	}
	return e
}

// source counts what the model asks, and answers with whatever is set.
type source struct {
	lists   int
	ports   []string
	askedOn []string
}

func (s *source) Source() Source {
	return Source{
		List: func(context.Context) ([]worktree.Entry, error) {
			s.lists++
			return nil, nil
		},
		Ports: func(_ context.Context, branch string) ([]string, error) {
			s.askedOn = append(s.askedOn, branch)
			return s.ports, nil
		},
	}
}

func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func loaded(t *testing.T, s *source, entries ...worktree.Entry) (Model, tea.Cmd) {
	t.Helper()
	return step(t, New(context.Background(), "repo", s.Source()),
		snapshotMsg{entries: entries, at: time.Date(2026, 9, 29, 14, 2, 31, 0, time.UTC)})
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func TestTheSelectionFollowsItsWorktreeAcrossAReorder(t *testing.T) {
	s := &source{}
	a, b := entry("feat/a", "up", 1), entry("feat/b", "down", 2)
	m, _ := loaded(t, s, a, b)
	m, _ = step(t, m, key("down"))
	m, _ = step(t, m, snapshotMsg{entries: []worktree.Entry{b, a}, at: time.Now()})
	if m.selected != b.Path {
		t.Fatalf("selected %q, want the worktree the cursor was on, %q", m.selected, b.Path)
	}
}

func TestTheSelectionFallsBackWhenItsWorktreeLeaves(t *testing.T) {
	s := &source{}
	a, b := entry("feat/a", "up", 1), entry("feat/b", "down", 2)
	m, _ := loaded(t, s, a, b)
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, snapshotMsg{entries: []worktree.Entry{a}, at: time.Now()})
	if m.selected != a.Path {
		t.Fatalf("selected %q, want the row that took its place, %q", m.selected, a.Path)
	}
}

// Docker can take longer to answer than the poll interval: a listing started
// while one runs would pile them up behind a slow daemon.
func TestOnlyOneListingRunsAtATime(t *testing.T) {
	s := &source{}
	m := New(context.Background(), "repo", s.Source())
	if _, cmd := step(t, m, key("r")); cmd != nil {
		t.Fatal("the first listing is still running: r must not start another")
	}
	m, _ = step(t, m, snapshotMsg{at: time.Now()})
	m, cmd := step(t, m, key("r"))
	if cmd == nil {
		t.Fatal("nothing runs any more: r must list again")
	}
	cmd()
	if s.lists != 1 || !m.loading {
		t.Fatalf("lists = %d, loading = %v: want one listing, marked running", s.lists, m.loading)
	}
}

func TestAFailedListingKeepsTheLastAnswer(t *testing.T) {
	s := &source{}
	m, _ := loaded(t, s, entry("feat/a", "up", 1))
	m, _ = step(t, m, snapshotMsg{err: errors.New("docker did not answer"), at: time.Now()})
	if len(m.entries) != 1 {
		t.Fatalf("entries = %v, want the last good answer kept", m.entries)
	}
	view := m.render()
	if !strings.Contains(view, "docker did not answer") || !strings.Contains(view, "feat/a") {
		t.Fatalf("the error and the last answer should both show:\n%s", view)
	}
}

func TestPortsAreAskedForTheSelectedWorktree(t *testing.T) {
	s := &source{ports: []string{"backend  http://localhost:8001"}}
	m, cmd := loaded(t, s, entry("feat/a", "up", 1), entry("feat/b", "up", 2))
	if cmd == nil {
		t.Fatal("the first row is selected: its ports should be asked for")
	}
	m, _ = step(t, m, cmd())
	if got := s.askedOn; len(got) != 1 || got[0] != "feat/a" {
		t.Fatalf("ports asked for %v, want only feat/a", got)
	}
	if !strings.Contains(m.render(), "http://localhost:8001") {
		t.Fatalf("the ports should show under the table:\n%s", m.render())
	}
}

// With no index recorded, `wtm ports` would survey docker and may record one:
// a dashboard only looks.
func TestPortsAreNotAskedForAWorktreeWithoutAnIndex(t *testing.T) {
	s := &source{}
	_, cmd := loaded(t, s, entry("feat/a", "down", 0))
	if cmd != nil {
		cmd()
	}
	if len(s.askedOn) != 0 {
		t.Fatalf("ports asked for %v, want none", s.askedOn)
	}
}

func TestAPortsAnswerForARowLeftBehindIsDropped(t *testing.T) {
	s := &source{}
	a, b := entry("feat/a", "up", 1), entry("feat/b", "up", 2)
	m, _ := loaded(t, s, a, b)
	m, _ = step(t, m, key("down"))
	m, _ = step(t, m, portsMsg{path: a.Path, lines: []string{"stale"}})
	if m.portsFor == a.Path || len(m.ports) != 0 {
		t.Fatalf("ports = %v for %q: an answer about feat/a must not show under feat/b", m.ports, m.portsFor)
	}
}

func TestTheCursorStopsAtBothEnds(t *testing.T) {
	s := &source{}
	a, b := entry("feat/a", "up", 1), entry("feat/b", "up", 2)
	m, _ := loaded(t, s, a, b)
	if m, cmd := step(t, m, key("up")); cmd != nil || m.selected != a.Path {
		t.Fatalf("up on the first row should do nothing, selected %q", m.selected)
	}
	m, _ = step(t, m, key("down"))
	if m, cmd := step(t, m, key("down")); cmd != nil || m.selected != b.Path {
		t.Fatalf("down on the last row should do nothing, selected %q", m.selected)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		msg := key(k)
		switch k {
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "ctrl+c":
			msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		}
		_, cmd := step(t, New(context.Background(), "repo", (&source{}).Source()), msg)
		if cmd == nil {
			t.Fatalf("%s should quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s should quit", k)
		}
	}
}

func TestTheViewNamesStatusBranchAndComposeProject(t *testing.T) {
	s := &source{}
	detached := entry("refactor/x", "down", 3)
	detached.Detached, detached.Head = true, "37a276b48e772823"
	curry := worktree.Entry{Worktree: stack.Worktree{Branch: "worktree-curry",
		Path: "/repo/.claude/worktrees/curry"}, Status: worktree.StatusAdoptable}
	m, _ := loaded(t, s, entry("feat/a", "up", 1), detached, curry)
	view := m.render()
	for _, want := range []string{"wtm · repo", "refreshed 14:02:31", "repo-wt-feat/a",
		"refactor/x (detached 37a276b4)", "adoptable", "› "} {
		if !strings.Contains(view, want) {
			t.Fatalf("view should contain %q:\n%s", want, view)
		}
	}
}

func TestTheViewSaysWhenThereIsNoWorktree(t *testing.T) {
	m, _ := loaded(t, &source{})
	if !strings.Contains(m.render(), "no worktree for repo") {
		t.Fatalf("an empty project should say so:\n%s", m.render())
	}
}

// A stack behind a proxy lists two dozen addresses: in a split terminal they
// pushed the keys off the screen.
func TestTheDetailGivesWayToTheKeysInAShortTerminal(t *testing.T) {
	s := &source{}
	for i := range 24 {
		s.ports = append(s.ports, fmt.Sprintf("svc%d  http://localhost:%d", i, 28000+i))
	}
	m, cmd := loaded(t, s, entry("feat/a", "up", 1), entry("feat/b", "down", 2))
	m, _ = step(t, m, cmd())
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 15})
	view := m.render()
	if got := strings.Count(view, "\n") + 1; got != 15 {
		t.Fatalf("the view takes %d rows, want the 15 the terminal has:\n%s", got, view)
	}
	if !strings.Contains(view, "q quit") || !strings.Contains(view, "`wtm ports feat/a`") {
		t.Fatalf("the keys must stay and the cut must say where the rest is:\n%s", view)
	}

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 60})
	if view := m.render(); !strings.Contains(view, "svc23") || strings.Contains(view, "more") {
		t.Fatalf("a tall terminal shows every address:\n%s", view)
	}
}
