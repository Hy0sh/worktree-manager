// Package execx wraps os/exec behind an interface, so command sequences can be
// asserted in tests without git, docker or node being installed.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd describes one external command invocation.
type Cmd struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string // extra variables, appended to the current environment
	Stdin io.Reader
	// Stdout, when set, receives the command output instead of it being
	// captured in Result (used to stream pg_dump straight to a file).
	Stdout io.Writer
	// Live mirrors output to the terminal, for long commands like docker build.
	Live bool
}

func (c Cmd) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}

// Result holds what a finished command produced.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner executes commands. Production code uses OSRunner, tests use Fake.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// Error carries the underlying command failure so callers can wrap it with
// their own context without losing the original message.
type Error struct {
	Cmd      string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("`%s` failed (exit %d): %s", e.Cmd, e.ExitCode, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// OSRunner runs commands for real.
type OSRunner struct {
	// Live output goes here; both default to the process streams.
	Stdout io.Writer
	Stderr io.Writer
}

func (r OSRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	cmd.Stdin = c.Stdin

	var outBuf, errBuf bytes.Buffer
	outs := []io.Writer{}
	if c.Stdout != nil {
		outs = append(outs, c.Stdout)
	} else {
		outs = append(outs, &outBuf)
	}
	if c.Live {
		outs = append(outs, or(r.Stdout, os.Stdout))
	}
	cmd.Stdout = io.MultiWriter(outs...)

	errs := []io.Writer{&errBuf}
	if c.Live {
		errs = append(errs, or(r.Stderr, os.Stderr))
	}
	cmd.Stderr = io.MultiWriter(errs...)

	runErr := cmd.Run()
	res := Result{Stdout: outBuf.String(), Stderr: errBuf.String()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if runErr != nil {
		return res, &Error{Cmd: c.String(), ExitCode: res.ExitCode, Stderr: res.Stderr, Err: runErr}
	}
	return res, nil
}

func or(w, fallback io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return fallback
}
