// Package tui is the `wtm tui` dashboard. It knows nothing of git, docker or
// the registry: everything it shows comes through the Source it is handed.
package tui

import (
	"context"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

// Source is what the dashboard asks, the same questions `wtm list` and
// `wtm ports` answer.
type Source struct {
	List           func(ctx context.Context) ([]worktree.Entry, error)
	Ports          func(ctx context.Context, branch string) ([]string, error)
	InspectRemoval func(ctx context.Context, branch string) (worktree.RemovalPlan, error)
	// Wtm runs this very binary. The lifecycle verbs are handed the terminal
	// rather than called in-process: docker streams straight to stdout, and a
	// start may ask about memory.
	Wtm func(args ...string) *exec.Cmd
	// Open hands a URL to the desktop's browser.
	Open func(url string) error
	// Memory is the warning `wtm start` prints when memory is tight, empty when
	// it is not. A start in the panel has no terminal to ask from, so the
	// dashboard asks before it runs.
	Memory func(ctx context.Context) (string, error)
}

// refreshEvery is also the floor between two listings: a docker slower than
// this delays the next one instead of stacking them up.
const refreshEvery = 5 * time.Second

// settleFor outlasts the replay of keys typed while a verb had the terminal.
const settleFor = 300 * time.Millisecond

type Model struct {
	ctx     context.Context
	project string
	src     Source

	entries []worktree.Entry
	// selected is a path and not a row: a refresh can reorder the rows, and a
	// worktree is where it stands.
	selected string
	loading  bool
	// again asks for one more listing once the running one answers: it may
	// have started before the action it would otherwise be taken to reflect.
	again bool
	err   error
	at    time.Time

	// away is set while a verb has the terminal, and settle is when keys count
	// again once it is back: what was typed at docker's output meanwhile is
	// replayed on return, and an enter there would open a shell.
	away   bool
	settle time.Time

	// note is the outcome of the last action, until the next one.
	note    string
	failed  bool
	confirm *removal
	memo    *memoryAsk
	// checking is a memory reading on its way to a start or an adoption.
	checking bool
	// job is the verb the panel shows, running or done, until esc or the next.
	job *job

	ports    []string
	portsFor string
	portsErr error

	// picking moves the cursor from the rows to the selected row's URLs, pick
	// being the one it is on.
	picking bool
	pick    int

	width, height int
}

type snapshotMsg struct {
	entries []worktree.Entry
	err     error
	at      time.Time
}

type tickMsg struct{}

type portsMsg struct {
	path  string
	lines []string
	err   error
}

// New starts loading: Init sends the first listing, so nothing else may.
func New(ctx context.Context, project string, src Source) Model {
	return Model{ctx: ctx, project: project, src: src, loading: true}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.list(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) list() tea.Cmd {
	ctx, list := m.ctx, m.src.List
	return func() tea.Msg {
		entries, err := list(ctx)
		return snapshotMsg{entries: entries, err: err, at: time.Now()}
	}
}

func (m *Model) refresh() tea.Cmd {
	if m.loading {
		return nil
	}
	m.loading = true
	return m.list()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		if m.away || time.Now().Before(m.settle) {
			return m, nil
		}
		if m.confirm != nil {
			return m, m.answer(msg.String())
		}
		if m.memo != nil {
			return m, m.answerMemory(msg.String())
		}
		if m.picking {
			return m, m.choose(msg.String())
		}
		if m.job != nil && m.job.running {
			if handled := m.whileRunning(msg.String()); handled {
				return m, nil
			}
		} else if m.job != nil && msg.String() == "esc" {
			m.job = nil
			return m, nil
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "s", "x", "d", "enter", "l", "a":
			return m, m.act(msg.String())
		case "up", "k":
			return m, m.move(-1)
		case "down", "j":
			return m, m.move(1)
		case "r":
			return m, m.refresh()
		case "o":
			if len(m.urls()) == 0 {
				m.tell(false, "no url to open for this worktree")
				return m, nil
			}
			m.picking, m.pick = true, 0
		}
	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())
	case actionMsg:
		m.away, m.settle = false, time.Now().Add(settleFor)
		return m, m.finish(msg)
	case memoryMsg:
		return m, m.checked(msg)
	case jobLineMsg:
		if m.job == nil {
			return m, nil
		}
		m.job.add(msg.line)
		return m, m.job.next()
	case jobEndMsg:
		if m.job == nil {
			return m, nil
		}
		j := m.job
		j.running, j.err = false, msg.err
		cmd := m.finish(actionMsg{verb: j.verb, branch: j.branch, err: msg.err})
		// Put away, a success has nothing left to show but its note, and a
		// failure has its reason to show.
		if j.hidden && (msg.err == nil || j.interrupted) {
			m.job = nil
		}
		j.hidden = false
		return m, cmd
	case removalMsg:
		m.ask(msg)
	case snapshotMsg:
		m.loading, m.at = false, msg.at
		var next tea.Cmd
		if m.again {
			m.again = false
			next = m.refresh()
		}
		// The last good answer stays on screen: a daemon that blinks out for
		// one poll is no reason to empty the dashboard.
		if msg.err != nil {
			m.err = msg.err
			return m, next
		}
		m.err = nil
		m.keepSelection(msg.entries)
		return m, tea.Batch(m.loadPorts(), next)
	case portsMsg:
		// Answered for a row the cursor has since left.
		if msg.path != m.selected {
			return m, nil
		}
		m.ports, m.portsErr, m.portsFor = msg.lines, msg.err, msg.path
		// A refresh can take URLs away from under the cursor.
		n := len(m.urls())
		m.picking = m.picking && n > 0
		m.pick = min(m.pick, max(n-1, 0))
	case openedMsg:
		if msg.err != nil {
			m.tell(true, "open %s: %v", msg.url, msg.err)
		} else {
			m.tell(false, "opened %s", msg.url)
		}
	}
	return m, nil
}

func (m Model) cursor() int {
	for i, e := range m.entries {
		if e.Path == m.selected {
			return i
		}
	}
	return -1
}

// keepSelection holds the cursor on its worktree, or on the row that took its
// place when the worktree is gone.
func (m *Model) keepSelection(entries []worktree.Entry) {
	pos := max(m.cursor(), 0)
	m.entries = entries
	if m.cursor() >= 0 {
		return
	}
	m.selected = ""
	if len(entries) > 0 {
		m.selected = entries[min(pos, len(entries)-1)].Path
	}
}

func (m *Model) move(delta int) tea.Cmd {
	if len(m.entries) == 0 {
		return nil
	}
	i := min(max(m.cursor()+delta, 0), len(m.entries)-1)
	if m.entries[i].Path == m.selected {
		return nil
	}
	m.selected = m.entries[i].Path
	return m.loadPorts()
}

// loadPorts asks only about a worktree whose index is recorded: for any other,
// `wtm ports` would survey docker and may record one, which a view must not.
func (m *Model) loadPorts() tea.Cmd {
	i := m.cursor()
	if i < 0 {
		m.ports, m.portsErr, m.portsFor = nil, nil, ""
		return nil
	}
	e := m.entries[i]
	if m.portsFor != e.Path {
		m.ports, m.portsErr, m.portsFor = nil, nil, ""
		m.picking = false
	}
	if e.ComposeProject == "" {
		m.portsFor = e.Path
		return nil
	}
	ctx, ports := m.ctx, m.src.Ports
	return func() tea.Msg {
		lines, err := ports(ctx, e.Branch)
		return portsMsg{path: e.Path, lines: lines, err: err}
	}
}
