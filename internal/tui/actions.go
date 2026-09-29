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

const comesBack = " · the dashboard comes back once it is done"

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
	switch {
	case e.Adoptable():
		m.tell(false, "not adopted: `wtm adopt %s` gives it a stack first", e.Branch)
		return nil
	case e.Branch == "":
		m.tell(true, "%s names no branch, and wtm addresses a worktree by its branch", e.Path)
		return nil
	}
	switch key {
	case "s":
		return m.run("start", e.Branch, "wtm start "+e.Branch+comesBack, true, "start", m.project, e.Branch)
	case "x":
		return m.run("stop", e.Branch, "wtm stop "+e.Branch+comesBack, true, "stop", m.project, e.Branch)
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
			"run", m.project, e.Branch, "--",
			"docker", "compose", "logs", "--follow", "--tail", "200")
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
		return m.run("remove", r.entry.Branch, "wtm "+strings.Join(args, " ")+comesBack, true, args...)
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
