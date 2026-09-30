package tui

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// panelKeep bounds what a job holds: a build can print thousands of lines,
// and only the last screenful is ever shown.
const panelKeep = 500

// job is a verb whose output the panel shows while the dashboard stays up.
// With no terminal, wtm asks nothing: the memory question is the dashboard's.
type job struct {
	verb, branch string
	cmd          *exec.Cmd
	events       <-chan tea.Msg
	lines        []string
	running      bool
	interrupted  bool
	// hidden puts the panel away while the job goes on: the ports are what a
	// start is waited on for, and the output is one key away.
	hidden bool
	err    error
}

type jobLineMsg struct{ line string }

type jobEndMsg struct{ err error }

// startJob runs c with both streams on one pipe, read until every writer is
// gone: docker compose interleaves the two, and reading them apart reorders it.
func startJob(verb, branch string, c *exec.Cmd) (*job, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdin, c.Stdout, c.Stderr = nil, w, w
	detach(c)
	if err := c.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	w.Close()
	events := make(chan tea.Msg, 64)
	go func() {
		defer r.Close()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		sc.Split(scanLines)
		for sc.Scan() {
			events <- jobLineMsg{line: sc.Text()}
		}
		events <- jobEndMsg{err: c.Wait()}
		close(events)
	}()
	return &job{verb: verb, branch: branch, cmd: c, events: events, running: true}, nil
}

// next waits for the job's next line, or its end.
func (j *job) next() tea.Cmd {
	events := j.events
	return func() tea.Msg { return <-events }
}

func (j *job) add(line string) {
	j.lines = append(j.lines, line)
	if len(j.lines) > panelKeep {
		j.lines = j.lines[len(j.lines)-panelKeep:]
	}
}

// interrupt asks the job to stop, and makes it on a second call: a compose
// that ignores the interrupt would otherwise hold the dashboard for good.
func (j *job) interrupt() (killed bool) {
	if !j.running {
		return false
	}
	if j.interrupted {
		_ = kill(j.cmd)
		return true
	}
	j.interrupted = true
	_ = interrupt(j.cmd)
	return false
}

// scanLines ends a line on \r as well as \n: a progress bar redraws itself
// with a carriage return, and each redraw is worth a line of its own here.
// Separators are skipped in the same call: a Scanner at EOF stops on a call
// that advances without a token, and the last line would be lost.
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	start := 0
	for start < len(data) && (data[start] == '\r' || data[start] == '\n') {
		start++
	}
	if i := bytes.IndexAny(data[start:], "\r\n"); i >= 0 {
		return start + i + 1, data[start : start+i], nil
	}
	if atEOF && start < len(data) {
		return len(data), data[start:], nil
	}
	return start, nil, nil
}
