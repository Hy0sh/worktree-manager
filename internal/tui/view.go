package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

var (
	faint    = lipgloss.NewStyle().Faint(true)
	bold     = lipgloss.NewStyle().Bold(true)
	failure  = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(1))
	statuses = map[string]lipgloss.Style{
		"up":                     lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(2)),
		"down":                   faint,
		worktree.StatusAdoptable: lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(3)),
		worktree.StatusUnknown:   faint,
	}
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "wtm · " + m.project
	return v
}

func (m Model) render() string {
	var b strings.Builder
	state := "loading…"
	switch {
	case m.loading && !m.at.IsZero():
		state = "refreshing…"
	case !m.at.IsZero():
		state = "refreshed " + m.at.Format("15:04:05")
	}
	b.WriteString(bold.Render("wtm · "+m.project) + "  " + faint.Render(state) + "\n\n")

	var detail []string
	switch {
	case m.at.IsZero() && m.err == nil:
	case len(m.entries) == 0 && m.err == nil:
		fmt.Fprintf(&b, "no worktree for %s (create one with `wtm create <branch>`)\n", m.project)
	default:
		m.table(&b)
		detail = m.detail()
	}

	var tail strings.Builder
	if m.err != nil {
		tail.WriteString("\n" + failure.Render("listing failed: "+m.err.Error()))
		if len(m.entries) > 0 {
			tail.WriteString(faint.Render(" (showing the last answer)"))
		}
		tail.WriteString("\n")
	}
	tail.WriteString("\n" + faint.Render("↑/↓ move · r refresh · q quit"))

	// The detail gives way, never the keys: a stack behind a proxy lists two
	// dozen addresses, more than a split terminal has rows for.
	if m.height > 0 {
		room := m.height - strings.Count(b.String(), "\n") - strings.Count(tail.String(), "\n") - 1
		detail = m.fit(detail, room)
	}
	for _, l := range detail {
		b.WriteString(l + "\n")
	}
	b.WriteString(tail.String())
	if m.width > 0 {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String())
	}
	return b.String()
}

// fit keeps what room allows, its last row saying what was left out.
func (m Model) fit(lines []string, room int) []string {
	if len(lines) <= room {
		return lines
	}
	if room <= 0 {
		return nil
	}
	cut := len(lines) - room + 1
	more := fmt.Sprintf("… %d more", cut)
	if e := m.entries[m.cursor()]; e.ComposeProject != "" {
		more += ", see `wtm ports " + e.Branch + "`"
	}
	return append(lines[:room-1:room-1], faint.Render(more))
}

// table pads every cell before styling it: an escape sequence has no width on
// screen, and counted as text it would push the columns apart.
func (m Model) table(b *strings.Builder) {
	header := []string{"INDEX", "BRANCH", "STATUS", "COMPOSE PROJECT"}
	rows := make([][]string, len(m.entries))
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len(h)
	}
	for i, e := range m.entries {
		idx, project := "-", "-"
		if e.Index > 0 {
			idx = strconv.Itoa(e.Index)
		}
		if e.ComposeProject != "" {
			project = e.ComposeProject
		}
		rows[i] = []string{idx, e.BranchLabel(), e.Status, project}
		for j, cell := range rows[i] {
			widths[j] = max(widths[j], lipgloss.Width(cell))
		}
	}
	pad := func(cells []string) []string {
		out := make([]string, len(cells))
		for j, cell := range cells {
			out[j] = cell + strings.Repeat(" ", widths[j]-lipgloss.Width(cell))
		}
		return out
	}

	b.WriteString("  " + faint.Render(strings.Join(pad(header), "  ")) + "\n")
	for i, e := range m.entries {
		cells := pad(rows[i])
		if st, ok := statuses[e.Status]; ok {
			cells[2] = st.Render(cells[2])
		}
		line := strings.Join(cells, "  ")
		if e.Path == m.selected {
			b.WriteString(bold.Render("› ") + line + "\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}
}

// detail opens on a blank row, which is what sets it apart from the table.
func (m Model) detail() []string {
	i := m.cursor()
	if i < 0 {
		return nil
	}
	e := m.entries[i]
	lines := []string{"", e.Path}
	switch {
	case e.Adoptable():
		return append(lines, faint.Render("not adopted: `wtm adopt "+e.Branch+"` gives it a stack where it stands"))
	case e.ComposeProject == "":
		return append(lines, faint.Render("no stack index recorded yet: its first start allocates one"))
	case m.portsFor != e.Path:
		return append(lines, faint.Render("ports…"))
	case m.portsErr != nil:
		return append(lines, failure.Render("ports: "+m.portsErr.Error()))
	case len(m.ports) == 0:
		return append(lines, faint.Render("no published port"))
	}
	for _, l := range m.ports {
		lines = append(lines, "  "+l)
	}
	return lines
}
