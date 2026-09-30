package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

type actionMsg struct {
	verb   string
	branch string
	err    error
}

type removalMsg struct {
	entry worktree.Entry
	plan  worktree.RemovalPlan
	err   error
}

// removal is a plan waiting for y or n, with the row it was asked from: the
// status is docker's, which the plan never asks.
type removal struct {
	entry worktree.Entry
	plan  worktree.RemovalPlan
}

func (m Model) target() (worktree.Entry, bool) {
	i := m.cursor()
	if i < 0 {
		return worktree.Entry{}, false
	}
	return m.entries[i], true
}

// act answers the action keys. Nothing but adopt has anything to say to an
// adoptable worktree, and a detached one outside wtm's root has no branch to
// name it by.
func (m *Model) act(key string) tea.Cmd {
	e, ok := m.target()
	if !ok {
		return nil
	}
	// A second start while the first is still reading memory would run both.
	if m.checking {
		m.tell(false, "still reading docker's memory…")
		return nil
	}
	switch {
	case e.Adoptable() && key == "a":
		// adopt refuses a detached HEAD: wtm keys a worktree by its branch.
		if e.Detached || e.Branch == "" {
			m.tell(true, "%s is on a detached HEAD: check a branch out there first "+
				"(`git -C %s switch -c <branch>`)", e.Path, e.Path)
			return nil
		}
		return m.readMemory("adopt", e)
	case e.Adoptable():
		m.tell(false, "not adopted: a gives it a stack first")
		return nil
	case key == "a":
		return nil
	case e.Branch == "":
		m.tell(true, "%s names no branch, and wtm addresses a worktree by its branch", e.Path)
		return nil
	}
	switch key {
	case "s":
		return m.readMemory("start", e)
	case "x":
		return m.launch("stop", e.Branch, "stop", m.project, e.Branch)
	case "d":
		ctx, inspect := m.ctx, m.src.InspectRemoval
		return func() tea.Msg {
			plan, err := inspect(ctx, e.Branch)
			return removalMsg{entry: e, plan: plan, err: err}
		}
	case "enter":
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "sh"
		}
		return m.run("shell", e.Branch, "shell in "+e.Path+" · exit returns to the dashboard", false,
			"run", m.project, e.Branch, "--", shell)
	case "l":
		// Following a stack that is down ends at once, which only blinks the
		// screen. An up stack also has an index, so COMPOSE_PROJECT_NAME is set.
		if e.Status != "up" || e.ComposeProject == "" {
			m.tell(false, "the stack of %s is not up: s starts it", e.Branch)
			return nil
		}
		return m.run("logs", e.Branch, "logs of "+e.Branch+" · ctrl+c returns to the dashboard", true,
			"logs", m.project, e.Branch)
	}
	return nil
}

type openedMsg struct {
	url string
	err error
}

// link is a URL `wtm ports` printed, and the port line it sits on.
type link struct {
	line int
	url  string
}

// urls are the selected row's addresses a browser takes: the address ends
// each port line, and one without a scheme is a database or a queue.
func (m Model) urls() []link {
	if m.portsFor == "" || m.portsFor != m.selected {
		return nil
	}
	var out []link
	for i, l := range m.ports {
		f := strings.Fields(l)
		if len(f) > 0 && (strings.HasPrefix(f[len(f)-1], "http://") || strings.HasPrefix(f[len(f)-1], "https://")) {
			out = append(out, link{i, f[len(f)-1]})
		}
	}
	return out
}

// choose answers the keys while the cursor is on the URLs.
func (m *Model) choose(key string) tea.Cmd {
	urls := m.urls()
	switch key {
	case "up", "k":
		m.pick = max(m.pick-1, 0)
	case "down", "j":
		m.pick = min(m.pick+1, len(urls)-1)
	case "enter":
		url, open := urls[m.pick].url, m.src.Open
		return func() tea.Msg { return openedMsg{url: url, err: open(url)} }
	case "o", "esc":
		m.picking = false
	case "ctrl+c":
		return tea.Quit
	}
	return nil
}

func (m *Model) ask(msg removalMsg) {
	if msg.err != nil {
		m.tell(true, "remove %s: %v", msg.entry.Branch, msg.err)
		return
	}
	m.confirm = &removal{entry: msg.entry, plan: msg.plan}
}

// answer is the only thing a pending removal listens to: a stray key must not
// start a stack behind the question.
func (m *Model) answer(key string) tea.Cmd {
	switch key {
	case "y":
		r := m.confirm
		m.confirm = nil
		args := []string{"remove", m.project, r.entry.Branch}
		if r.plan.RequiresForce() {
			args = []string{"remove", "--force", m.project, r.entry.Branch}
		}
		return m.launch("remove", r.entry.Branch, args...)
	case "n", "esc", "q":
		m.confirm = nil
		m.tell(false, "nothing removed")
	case "ctrl+c":
		return tea.Quit
	}
	return nil
}

// run hands the terminal to wtm. pause keeps a failure on screen until enter:
// the dashboard would otherwise take the screen back over the one line saying
// why. A shell's status is its last command's, which is nobody's failure.
func (m *Model) run(verb, branch, banner string, pause bool, args ...string) tea.Cmd {
	c := m.src.Wtm(args...)
	m.away = true
	var ec tea.ExecCommand = &held{cmd: c, what: verb + " " + branch, banner: banner, pause: pause}
	return tea.Exec(ec, func(err error) tea.Msg {
		return actionMsg{verb: verb, branch: branch, err: err}
	})
}

// finish notes how a verb ended and lists again: the listing running now may
// have started before the verb, and another follows it then.
func (m *Model) finish(msg actionMsg) tea.Cmd {
	m.done(msg)
	cmd := m.refresh()
	m.again = cmd == nil
	return cmd
}

func (m *Model) done(msg actionMsg) {
	quiet := msg.verb == "shell" || msg.verb == "logs"
	switch {
	case msg.err == nil && quiet, msg.verb == "shell", msg.verb == "logs" && interrupted(msg.err):
		m.note, m.failed = "", false
	case msg.err == nil:
		m.tell(false, "%s %s: done", msg.verb, msg.branch)
	case interrupted(msg.err):
		m.tell(true, "%s %s interrupted", msg.verb, msg.branch)
	default:
		m.tell(true, "%s %s failed: %v", msg.verb, msg.branch, msg.err)
	}
}

func (m *Model) tell(failed bool, format string, args ...any) {
	m.note, m.failed = fmt.Sprintf(format, args...), failed
}

// interrupted says ctrl+c ended the command, which is how logs are left.
func interrupted(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == -1
}

// held is the command tea.Exec runs, pausing on a failure before the dashboard
// takes the terminal back.
type held struct {
	cmd  *exec.Cmd
	what string
	// banner heads a cleared screen: without it, what the command prints lands
	// under what the dashboard left there, and a shell prompt reads as the end.
	banner string
	pause  bool
}

func (h *held) SetStdin(r io.Reader)  { h.cmd.Stdin = r }
func (h *held) SetStdout(w io.Writer) { h.cmd.Stdout = w }
func (h *held) SetStderr(w io.Writer) { h.cmd.Stderr = w }

func (h *held) Run() error {
	if h.banner != "" && h.cmd.Stdout != nil {
		fmt.Fprintf(h.cmd.Stdout, "\x1b[H\x1b[2J%s\n\n", faint.Render(h.banner))
	}
	err := h.cmd.Run()
	if err == nil || !h.pause || interrupted(err) {
		return err
	}
	fmt.Fprintf(h.cmd.Stderr, "\n%s failed (%v): press enter to go back to the dashboard ", h.what, err)
	if h.cmd.Stdin != nil {
		_, _ = bufio.NewReader(h.cmd.Stdin).ReadString('\n')
	}
	return err
}

type memoryMsg struct {
	verb    string
	entry   worktree.Entry
	warning string
	err     error
}

// memoryAsk is the question `wtm start` and `wtm adopt` ask at a terminal,
// asked here instead: the verb in the panel has none, and adopt without one
// refuses to write into a checkout it did not create.
type memoryAsk struct {
	verb    string
	entry   worktree.Entry
	warning string
}

// readMemory takes docker a few seconds, stats included: the note says so,
// or the key reads as ignored and the next one is typed at nothing.
func (m *Model) readMemory(verb string, e worktree.Entry) tea.Cmd {
	m.checking = true
	m.tell(false, "reading docker's memory before the %s…", verb)
	ctx, memory := m.ctx, m.src.Memory
	return func() tea.Msg {
		warning, err := memory(ctx)
		return memoryMsg{verb: verb, entry: e, warning: warning, err: err}
	}
}

// checked starts at once unless memory is tight. An unreadable reading does
// not stop a start either: wtm start itself goes ahead on one. An adoption is
// always asked about, tight memory or not.
func (m *Model) checked(msg memoryMsg) tea.Cmd {
	m.checking, m.note = false, ""
	if msg.err != nil {
		msg.warning = ""
	}
	if msg.verb == "start" && msg.warning == "" {
		return m.launch("start", msg.entry.Branch, "start", m.project, msg.entry.Branch)
	}
	m.memo = &memoryAsk{verb: msg.verb, entry: msg.entry, warning: msg.warning}
	return nil
}

func (m *Model) answerMemory(key string) tea.Cmd {
	a := m.memo
	switch key {
	case "y":
		m.memo = nil
		args := []string{a.verb}
		if a.verb == "adopt" {
			args = append(args, "-y")
		}
		if a.warning != "" {
			args = append(args, "--ignore-memory")
		}
		return m.launch(a.verb, a.entry.Branch, append(args, m.project, a.entry.Branch)...)
	case "n", "esc", "q":
		m.memo = nil
		if a.verb == "adopt" {
			m.tell(false, "%s not adopted: nothing was written", a.entry.Branch)
		} else {
			m.tell(false, "stack of %s not started: free some memory first", a.entry.Branch)
		}
	case "ctrl+c":
		return tea.Quit
	}
	return nil
}

// launch runs a verb in the panel.
func (m *Model) launch(verb, branch string, args ...string) tea.Cmd {
	j, err := startJob(verb, branch, m.src.Wtm(args...))
	if err != nil {
		m.tell(true, "%s %s: %v", verb, branch, err)
		return nil
	}
	m.job, m.note, m.failed = j, "", false
	return j.next()
}

// whileRunning answers the keys a running job reserves, and leaves moving,
// refreshing and the URLs to the dashboard. Quitting would cut the job's
// output off, and compose writing to a closed pipe dies halfway.
func (m *Model) whileRunning(key string) bool {
	j := m.job
	switch key {
	case "ctrl+c":
		if j.interrupt() {
			m.tell(true, "killing %s %s", j.verb, j.branch)
		} else {
			m.tell(false, "interrupting %s %s… (ctrl+c again kills it)", j.verb, j.branch)
		}
	case "esc":
		j.hidden = true
	case "p":
		j.hidden = false
	case "q":
		m.tell(false, "%s %s is running: ctrl+c interrupts it", j.verb, j.branch)
	case "s", "x", "d", "enter", "l", "a":
		m.tell(false, "%s %s is still running: wait for it, or ctrl+c", j.verb, j.branch)
	default:
		return false
	}
	return true
}
