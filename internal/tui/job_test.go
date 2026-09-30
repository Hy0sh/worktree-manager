package tui

import (
	"bufio"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// drain plays cmd's messages into m until the job it started ends, the way
// the program would, and returns the model as it stands then.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for cmd != nil {
		msgs := make(chan tea.Msg, 1)
		go func(c tea.Cmd) { msgs <- c() }(cmd)
		var msg tea.Msg
		select {
		case msg = <-msgs:
		case <-deadline:
			t.Fatal("the job never ended")
		}
		m, cmd = step(t, m, msg)
		if _, ended := msg.(jobEndMsg); ended {
			return m
		}
		// A listing or a ports answer can come between two lines: only the
		// job's own messages lead on.
		switch msg.(type) {
		case jobLineMsg, memoryMsg:
		default:
			return m
		}
	}
	return m
}

func TestAStartShowsItsOutputUnderTheTable(t *testing.T) {
	s := &source{script: `echo "Container db Creating"; echo "warning on stderr" >&2; printf 'pull 50%%\rpull 100%%\n'`}
	m, _ := loaded(t, s, entry("feat/a", "down", 1))
	m, cmd := step(t, m, key("s"))
	m = drain(t, m, cmd)
	if got, want := m.job.lines, []string{"Container db Creating", "warning on stderr", "pull 50%", "pull 100%"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
	view := m.render()
	for _, want := range []string{"INDEX", "wtm start feat/a", "done", "pull 100%", "esc close the output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the table, the header and the output should all show, missing %q:\n%s", want, view)
		}
	}
	if m.note != "start feat/a: done" {
		t.Fatalf("note = %q", m.note)
	}
	if m, _ = step(t, m, key("esc")); m.job != nil {
		t.Fatal("esc should close a finished output")
	}
}

// With no terminal behind it, wtm start asks nothing: a start that went ahead
// on tight memory unasked would drop the one guard it has.
func TestTightMemoryIsAskedAboutBeforeTheStart(t *testing.T) {
	s := &source{memory: "warning: 7.5 GiB of 8 GiB in use"}
	m, _ := loaded(t, s, entry("feat/a", "down", 1))
	m, cmd := step(t, m, key("s"))
	m, _ = step(t, m, cmd())
	ran(t, s)
	view := m.render()
	for _, want := range []string{"start feat/a?", "7.5 GiB", "y start anyway"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the question should say %q:\n%s", want, view)
		}
	}
	m, _ = step(t, m, key("n"))
	ran(t, s)
	if m.memo != nil || !strings.Contains(m.note, "not started") {
		t.Fatalf("n should leave the stack down and say so, note %q", m.note)
	}

	m, cmd = step(t, m, key("s"))
	m, _ = step(t, m, cmd())
	m, cmd = step(t, m, key("y"))
	drain(t, m, cmd)
	ran(t, s, []string{"start", "--ignore-memory", "repo", "feat/a"})
}

func TestAFailedJobKeepsItsOutputOnScreen(t *testing.T) {
	s := &source{script: `echo "Bind for 0.0.0.0:28001 failed: port is already allocated" >&2; exit 1`}
	m, _ := loaded(t, s, entry("feat/a", "down", 1))
	m, cmd := step(t, m, key("x"))
	m = drain(t, m, cmd)
	view := m.render()
	if !strings.Contains(view, "port is already allocated") || !strings.Contains(view, "failed: exit status 1") {
		t.Fatalf("the reason and the failure should stay on screen:\n%s", view)
	}
	if !m.failed {
		t.Fatalf("note = %q, want a failure", m.note)
	}
}

// Quitting mid-job would close the pipe compose writes to, and a compose that
// dies on it leaves the stack half up.
func TestARunningJobHoldsTheVerbsAndTheQuit(t *testing.T) {
	s := &source{script: "echo started; sleep 5"}
	m, _ := loaded(t, s, entry("feat/a", "down", 1))
	m, cmd := step(t, m, key("x"))
	m, cmd = step(t, m, cmd())
	if m.job == nil || !m.job.running {
		t.Fatal("the job should be running")
	}
	for _, k := range []string{"s", "d", "l", "q", "esc"} {
		var c tea.Cmd
		if m, c = step(t, m, key(k)); c != nil {
			t.Fatalf("%s should be held while stop runs", k)
		}
	}
	ran(t, s, []string{"stop", "repo", "feat/a"})
	if !strings.Contains(m.note, "ctrl+c interrupts it") {
		t.Fatalf("note = %q, want the way out", m.note)
	}
	if !strings.Contains(m.render(), "ctrl+c interrupt stop") {
		t.Fatalf("the key line should offer the interrupt:\n%s", m.render())
	}

	// A shell defers an interrupt taken before it forks, and would then start
	// the sleep regardless: wtm, the real job, has no such window.
	time.Sleep(200 * time.Millisecond)
	begun := time.Now()
	m, _ = step(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = drain(t, m, cmd)
	if time.Since(begun) > 3*time.Second {
		t.Fatal("the interrupt should reach the sleep the script runs, not the shell alone")
	}
	if m.note != "stop feat/a interrupted" || !strings.Contains(m.render(), "interrupted") {
		t.Fatalf("note = %q:\n%s", m.note, m.render())
	}
}

func TestASecondCtrlCKillsAJobThatIgnoresTheFirst(t *testing.T) {
	s := &source{script: "trap '' INT; echo started; sleep 5"}
	m, _ := loaded(t, s, entry("feat/a", "down", 1))
	m, cmd := step(t, m, key("x"))
	m, cmd = step(t, m, cmd())
	time.Sleep(200 * time.Millisecond)
	begun := time.Now()
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	m, _ = step(t, m, ctrlC)
	if !strings.Contains(m.note, "ctrl+c again kills it") {
		t.Fatalf("note = %q, want the way to force it", m.note)
	}
	m, _ = step(t, m, ctrlC)
	m = drain(t, m, cmd)
	if time.Since(begun) > 3*time.Second || m.job.running {
		t.Fatal("the second ctrl+c should end the job at once")
	}
}

// Only the tail of a long output fits, and the tail is what says how it went.
func TestTheOutputKeepsItsLastLinesInAShortTerminal(t *testing.T) {
	s := &source{script: "for i in $(seq 1 40); do echo line$i; done"}
	m, _ := loaded(t, s, entry("feat/a", "down", 1))
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m, cmd := step(t, m, key("x"))
	m = drain(t, m, cmd)
	view := m.render()
	if got := strings.Count(view, "\n") + 1; got != 20 {
		t.Fatalf("the view takes %d rows, want 20:\n%s", got, view)
	}
	if !strings.Contains(view, "wtm stop feat/a") || !strings.Contains(view, "line40") || strings.Contains(view, "line1\n") {
		t.Fatalf("the header and the latest lines should stay, the first go:\n%s", view)
	}
}

func TestScanLinesSplitsOnCarriageReturnsToo(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("a\r\nb\rc\n\nd"))
	sc.Split(scanLines)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A start is waited on for its ports: esc puts the output away and leaves the
// start going, and a failure brings its reason back on its own.
func TestEscPutsARunningOutputAwayWithoutStoppingIt(t *testing.T) {
	for _, c := range []struct {
		name, script string
		shown        bool
	}{
		{"success", "echo started; sleep 0.3", false},
		{"failure", "echo started; sleep 0.3; echo 'port is already allocated' >&2; exit 1", true},
	} {
		s := &source{script: c.script, ports: []string{"backend  http://localhost:28001"}}
		m, portsCmd := loaded(t, s, entry("feat/a", "down", 1))
		m, _ = step(t, m, portsCmd())
		m, cmd := step(t, m, key("x"))
		m, cmd = step(t, m, cmd())
		m, _ = step(t, m, key("esc"))
		if m.job == nil || !m.job.running {
			t.Fatalf("%s: esc must not stop the job", c.name)
		}
		view := m.render()
		if !strings.Contains(view, "localhost:28001") || !strings.Contains(view, "wtm stop feat/a running…") ||
			!strings.Contains(view, "p show the output") {
			t.Fatalf("%s: the ports should come back with the job named under them:\n%s", c.name, view)
		}
		m, _ = step(t, m, key("p"))
		if m.job.hidden {
			t.Fatalf("%s: p should bring the output back", c.name)
		}
		m, _ = step(t, m, key("esc"))
		m = drain(t, m, cmd)
		if shown := m.job != nil && strings.Contains(m.render(), "port is already allocated"); shown != c.shown {
			t.Fatalf("%s: output shown = %v, want %v:\n%s", c.name, shown, c.shown, m.render())
		}
	}
}

// Cut at the edge of a narrow split, the key line lost its last keys, quit
// and refresh among them.
func TestTheKeyLineWrapsBetweenKeysInANarrowTerminal(t *testing.T) {
	m, _ := loaded(t, &source{}, entry("feat/a", "up", 1))
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 50, Height: 30})
	view := m.render()
	for _, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 50 {
			t.Fatalf("a line is %d wide, past the 50 of the terminal: %q", w, line)
		}
	}
	for _, k := range []string{"s start", "l logs", "r refresh", "q quit"} {
		if !strings.Contains(view, k) {
			t.Fatalf("%q should survive the wrap:\n%s", k, view)
		}
	}
	if got := strings.Count(view, "\n") + 1; got > 30 {
		t.Fatalf("the view takes %d rows, past the 30 the terminal has", got)
	}
}

func TestWrapKeysLeavesAFittingLineAlone(t *testing.T) {
	if got := wrapKeys("r refresh · q quit", 80); !reflect.DeepEqual(got, []string{"r refresh · q quit"}) {
		t.Fatalf("got %q", got)
	}
	if got, want := wrapKeys("a · bb · ccc", 6), []string{"a · bb", "ccc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
