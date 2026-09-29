// Package tui is the `wtm tui` dashboard. It knows nothing of git, docker or
// the registry: everything it shows comes through the Source it is handed.
package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

// Source is what the dashboard asks, the same questions `wtm list` and
// `wtm ports` answer.
type Source struct {
	List  func(ctx context.Context) ([]worktree.Entry, error)
	Ports func(ctx context.Context, branch string) ([]string, error)
}

// refreshEvery is also the floor between two listings: a docker slower than
// this delays the next one instead of stacking them up.
const refreshEvery = 5 * time.Second

type Model struct {
	ctx     context.Context
	project string
	src     Source

	entries []worktree.Entry
	// selected is a path and not a row: a refresh can reorder the rows, and a
	// worktree is where it stands.
	selected string
	loading  bool
	err      error
	at       time.Time

	ports    []string
	portsFor string
	portsErr error

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
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			return m, m.move(-1)
		case "down", "j":
			return m, m.move(1)
		case "r":
			return m, m.refresh()
		}
	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())
	case snapshotMsg:
		m.loading, m.at = false, msg.at
		// The last good answer stays on screen: a daemon that blinks out for
		// one poll is no reason to empty the dashboard.
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.keepSelection(msg.entries)
		return m, m.loadPorts()
	case portsMsg:
		// Answered for a row the cursor has since left.
		if msg.path != m.selected {
			return m, nil
		}
		m.ports, m.portsErr, m.portsFor = msg.lines, msg.err, msg.path
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
