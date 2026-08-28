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
	"time"
)

type Cmd struct {
	Name string
	Args []string
	Dir  string
	Env  []string // extra variables, appended to the current environment
	// Stdout, when set, receives the command output instead of it being
	// captured in Result (used to stream pg_dump straight to a file).
	Stdout io.Writer
	// Live mirrors output to the terminal, for long commands like docker build.
	Live bool
	// Interactive hands the process the terminal itself, so that a shell or
	// any prompt-driven command behaves normally. Nothing is captured.
	Interactive bool
}

// String renders the command the way a shell would take it back, because wtm
// prints commands for the user to replay: joining the arguments raw turns
// `sh -c "a && b"` into two commands and a path with a space into two
// arguments.
func (c Cmd) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, ShellQuote(c.Name))
	for _, a := range c.Args {
		parts = append(parts, ShellQuote(a))
	}
	return strings.Join(parts, " ")
}

// ShellQuote wraps s in single quotes unless every character is one a shell
// takes literally, which covers paths, flags and key=value.
func ShellQuote(s string) string {
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-/:=@+,"
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune(safe, r) }) < 0 {
		return s
	}
	// The only escape a POSIX shell has for a single quote inside single
	// quotes: close, emit an escaped one, reopen.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

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

// OSRunner is the production runner. Live output goes to the process streams;
// a command wanting its output elsewhere sets Cmd.Stdout.
type OSRunner struct{}

func (r OSRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}

	// An interactive command needs the real terminal on all three streams:
	// capturing output would break line editing, and without stdin a shell
	// exits immediately.
	if c.Interactive {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		runErr := cmd.Run()
		var res Result
		if cmd.ProcessState != nil {
			res.ExitCode = cmd.ProcessState.ExitCode()
		}
		if runErr != nil {
			return res, &Error{Cmd: c.String(), ExitCode: res.ExitCode, Err: runErr}
		}
		return res, nil
	}

	var outBuf, errBuf bytes.Buffer
	outs := []io.Writer{}
	if c.Stdout != nil {
		outs = append(outs, c.Stdout)
	} else {
		outs = append(outs, &outBuf)
	}
	if c.Live {
		outs = append(outs, os.Stdout)
	}
	cmd.Stdout = io.MultiWriter(outs...)

	errs := []io.Writer{&errBuf}
	if c.Live {
		errs = append(errs, os.Stderr)
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

// WaitFor runs probe until it succeeds, which is how a container is told to be
// ready: docker reports one started long before what runs inside it answers.
// The last failure is wrapped, since it is the only thing that says why.
func WaitFor(ctx context.Context, r Runner, label string, attempts int, interval time.Duration, probe Cmd) error {
	var last error
	for i := 0; i < attempts; i++ {
		if _, err := r.Run(ctx, probe); err == nil {
			return nil
		} else {
			last = err
		}
		if i < attempts-1 && interval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
	}
	return fmt.Errorf("timed out waiting for %s (%d attempts): %w", label, attempts, last)
}
