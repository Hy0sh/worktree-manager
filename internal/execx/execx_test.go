package execx

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOSRunnerCapturesStdout(t *testing.T) {
	res, err := OSRunner{}.Run(context.Background(), Cmd{Name: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hello")
	}
}

func TestOSRunnerStreamsToWriter(t *testing.T) {
	var buf bytes.Buffer
	res, err := OSRunner{}.Run(context.Background(), Cmd{
		Name:   "echo",
		Args:   []string{"streamed"},
		Stdout: &buf,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "streamed" {
		t.Fatalf("writer got %q", buf.String())
	}
	if res.Stdout != "" {
		t.Fatalf("stdout should not be captured when Stdout is set, got %q", res.Stdout)
	}
}

func TestOSRunnerFailurePropagatesStderr(t *testing.T) {
	_, err := OSRunner{}.Run(context.Background(), Cmd{
		Name: "sh",
		Args: []string{"-c", "echo boom >&2; exit 3"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	var execErr *Error
	if !errors.As(err, &execErr) {
		t.Fatalf("expected *execx.Error, got %T", err)
	}
	if execErr.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", execErr.ExitCode)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should carry stderr, got %q", err.Error())
	}
}

func TestFakeRecordsCallsAndWritesStdout(t *testing.T) {
	f := &Fake{Handler: func(c Cmd) (Result, error) {
		return Result{Stdout: "payload"}, nil
	}}
	var sink bytes.Buffer
	if _, err := f.Run(context.Background(), Cmd{Name: "git", Args: []string{"status"}, Dir: "/repo", Stdout: &sink}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.Lines(); len(got) != 1 || got[0] != "git status" {
		t.Fatalf("Lines() = %v", got)
	}
	if f.Calls[0].Dir != "/repo" {
		t.Fatalf("Dir = %q", f.Calls[0].Dir)
	}
	if sink.String() != "payload" {
		t.Fatalf("sink = %q", sink.String())
	}
}

// The replay messages wtm prints are meant to be pasted back into a shell.
func TestCmdStringQuotesWhatAShellWouldSplit(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "seed && users"}, `sh -c 'seed && users'`},
		{[]string{"/a path/x"}, `sh '/a path/x'`},
		{[]string{"-c", "echo it's here"}, `sh -c 'echo it'\''s here'`},
		{[]string{"compose", "-p", "app-wt-1", "exec", "-T", "backend"}, "sh compose -p app-wt-1 exec -T backend"},
	} {
		if got := (Cmd{Name: "sh", Args: tc.args}).String(); got != tc.want {
			t.Errorf("String() =\n  %s\nwant\n  %s", got, tc.want)
		}
	}
}

func TestFailureNamesACommandAShellTakesBack(t *testing.T) {
	_, err := OSRunner{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "exit 1 && nope"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `sh -c 'exit 1 && nope'`) {
		t.Fatalf("error should quote the command, got %q", err.Error())
	}
}

// A command that never started has no exit status: reporting the zero value of
// the field would read as a success.
func TestFailureBeforeStartNamesTheMissingBinary(t *testing.T) {
	_, err := OSRunner{}.Run(context.Background(), Cmd{Name: "wtm-no-such-binary", Args: []string{"down"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "exit") {
		t.Fatalf("error should not claim an exit code, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "wtm-no-such-binary is not installed") {
		t.Fatalf("error should name the missing binary, got %q", err.Error())
	}
}

func TestMissingBinaryOnlyAnswersForALookupFailure(t *testing.T) {
	_, err := OSRunner{}.Run(context.Background(), Cmd{Name: "wtm-no-such-binary"})
	if got := MissingBinary(err); got != "wtm-no-such-binary" {
		t.Fatalf("MissingBinary = %q, want the binary name", got)
	}
	_, err = OSRunner{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "exit 3"}})
	if got := MissingBinary(err); got != "" {
		t.Fatalf("MissingBinary = %q for a command that ran and failed", got)
	}
}
