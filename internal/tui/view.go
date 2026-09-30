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
		switch {
		case m.confirm != nil:
			detail = m.confirmation()
		case m.memo != nil:
			detail = m.memoryQuestion()
		case m.job != nil && !m.job.hidden:
			detail = m.panel()
		}
	}

	var tail strings.Builder
	if m.err != nil {
		tail.WriteString("\n" + failure.Render("listing failed: "+m.err.Error()))
		if len(m.entries) > 0 {
			tail.WriteString(faint.Render(" (showing the last answer)"))
		}
		tail.WriteString("\n")
	}
	if m.job != nil && m.job.hidden {
		tail.WriteString("\n" + faint.Render("wtm "+m.job.verb+" "+m.job.branch+" running…") + "\n")
	}
	if m.note != "" {
		note := faint
		if m.failed {
			note = failure
		}
		tail.WriteString("\n" + note.Render(m.note) + "\n")
	}
	keys := m.keys()
	switch {
	case m.confirm != nil:
		keys = "y remove · n keep it"
		if m.confirm.plan.RequiresForce() {
			keys = "y remove with --force · n keep it"
		}
	case m.memo != nil:
		keys = "y start anyway · n leave it down"
	}
	for _, l := range wrapKeys(keys, m.width) {
		tail.WriteString("\n" + faint.Render(l))
	}

	// The detail gives way, never the keys: a stack behind a proxy lists two
	// dozen addresses, more than a split terminal has rows for.
	if m.height > 0 {
		room := m.height - strings.Count(b.String(), "\n") - strings.Count(tail.String(), "\n") - 1
		switch {
		// Output reads from the bottom: the last lines are the ones to keep.
		case m.job != nil && !m.job.hidden && m.confirm == nil && m.memo == nil:
			detail = lastLines(detail, room)
		default:
			// The URL under the cursor stays above the cut, the top giving way.
			if r := m.pickRow(); room > 1 && r >= room-1 {
				detail = detail[r-room+2:]
			}
			detail = m.fit(detail, room)
		}
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
	if e, ok := m.target(); ok && e.ComposeProject != "" && m.confirm == nil {
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
	urls := m.urls()
	at := map[int]string{}
	for _, u := range urls {
		at[u.line] = u.url
	}
	for i, l := range m.ports {
		prefix := "  "
		if m.picking && urls[m.pick].line == i {
			prefix = bold.Render("› ")
		}
		// OSC 8 makes the address clickable where the terminal knows it, and
		// changes nothing on screen where it does not.
		if url, ok := at[i]; ok {
			l = strings.TrimSuffix(l, url) + lipgloss.NewStyle().Hyperlink(url).Render(url)
		}
		lines = append(lines, prefix+l)
	}
	return lines
}

// pickRow is where the URL under the cursor stands in the detail, which opens
// on a blank row and the worktree's path; -1 while the cursor is on the rows.
func (m Model) pickRow() int {
	if !m.picking {
		return -1
	}
	return 2 + m.urls()[m.pick].line
}

// confirmation says what y is about to take, from the plan Remove itself acts
// on: what calls for --force is never decided here.
func (m Model) confirmation() []string {
	r := m.confirm
	lines := []string{"", bold.Render("remove " + r.entry.BranchLabel() + "?")}
	switch r.plan.Kind {
	case worktree.RemoveCreated:
		lines = append(lines, "the directory goes, with the stack, its volumes and built images (branch kept)")
	case worktree.RemoveAdopted:
		lines = append(lines, "the stack goes with its volumes, and wtm's own files: the checkout stays (branch kept)")
	}
	if r.entry.Status == "up" {
		lines = append(lines, "its stack is up, and comes down first")
	}
	if r.plan.Locked {
		lines = append(lines, failure.Render("locked"+lockReason(r.plan.LockReason)))
	}
	if r.plan.Changes != "" {
		lines = append(lines, failure.Render("uncommitted changes, lost with the directory:"))
		for _, c := range strings.Split(r.plan.Changes, "\n") {
			lines = append(lines, "  "+c)
		}
	}
	return lines
}

func lockReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// keys offers only what the selected row can take: on an empty project, a
// worktree left to adopt or a stack that is down, a key would only be refused.
func (m Model) keys() string {
	e, ok := m.target()
	open := ""
	if len(m.urls()) > 0 {
		open = " · o open a url"
	}
	closing := ""
	if m.job != nil {
		closing = " · esc close the output"
	}
	switch {
	case m.picking:
		return "↑/↓ choose · enter open in the browser · esc back to the worktrees"
	case m.job != nil && m.job.hidden:
		return "↑/↓ move · p show the output · ctrl+c interrupt " + m.job.verb + open + " · r refresh"
	case m.job != nil && m.job.running:
		return "↑/↓ move · esc hide the output · ctrl+c interrupt " + m.job.verb + open + " · r refresh"
	case !ok:
		return "r refresh · q quit" + closing
	case e.Adoptable() || e.Branch == "":
		return "↑/↓ move · r refresh · q quit" + closing
	case e.Status != "up" || e.ComposeProject == "":
		return "↑/↓ move · s start · x stop · d remove · enter shell" + open + " · r refresh · q quit" + closing
	}
	return "↑/↓ move · s start · x stop · d remove · enter shell · l logs" + open + " · r refresh · q quit" + closing
}

// panel is the job's output under a header saying where it stands.
func (m Model) panel() []string {
	j := m.job
	state := faint.Render("running…")
	switch {
	case j.running:
	case j.interrupted:
		state = failure.Render("interrupted")
	case j.err != nil:
		state = failure.Render("failed: " + j.err.Error())
	default:
		state = statuses["up"].Render("done")
	}
	lines := []string{"", bold.Render("wtm "+j.verb+" "+j.branch) + "  " + state}
	for _, l := range j.lines {
		lines = append(lines, "  "+l)
	}
	return lines
}

// lastLines keeps the blank row and the header, then the latest output room
// allows.
func lastLines(lines []string, room int) []string {
	const head = 2
	if len(lines) <= room {
		return lines
	}
	if room <= head {
		return lines[:max(room, 0)]
	}
	return append(lines[:head:head], lines[len(lines)-(room-head):]...)
}

// memoryQuestion is what `wtm start` would have asked at a terminal.
func (m Model) memoryQuestion() []string {
	return []string{"", bold.Render("start " + m.memo.entry.BranchLabel() + "?"),
		m.memo.warning,
		faint.Render("y starts it anyway, as `wtm start --ignore-memory` does")}
}

// wrapKeys breaks the key line between two keys, never inside one: cut at the
// edge, the last keys, quit among them, were the ones a narrow split lost.
func wrapKeys(keys string, width int) []string {
	if width <= 0 || lipgloss.Width(keys) <= width {
		return []string{keys}
	}
	var lines []string
	line := ""
	for _, k := range strings.Split(keys, " · ") {
		switch {
		case line == "":
			line = k
		case lipgloss.Width(line+" · "+k) <= width:
			line += " · " + k
		default:
			lines = append(lines, line)
			line = k
		}
	}
	return append(lines, line)
}
