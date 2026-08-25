package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// The published version is only worth a line when this build is behind it: a
// local build sitting on a tag the proxy has not seen yet must say nothing.
func TestNewerReleaseOnlyReportsWhatIsAhead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		local    string
		answer   string
		reported bool
	}{
		{name: "behind", local: "v0.4.7", answer: "v0.4.8", reported: true},
		{name: "current", local: "v0.4.8", answer: "v0.4.8"},
		{name: "ahead", local: "v0.4.9", answer: "v0.4.8"},
		{name: "working copy", local: "devel", answer: "v0.4.8"},
		{name: "local edits over the tag", local: "v0.4.7+dirty", answer: "v0.4.8", reported: true},
		{name: "minor behind", local: "v0.4.9", answer: "v0.5.0", reported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := olderVersion(tc.local, tc.answer); got != tc.reported {
				t.Fatalf("olderVersion(%q, %q) = %v", tc.local, tc.answer, got)
			}
		})
	}
}

// An unreachable proxy, a slow one or a body that is not what the proxy sends
// must all leave `wtm doctor` exactly as it was: no line, no error.
func TestNewerReleaseSaysNothingOnAnUnusableAnswer(t *testing.T) {
	for _, body := range []string{"", "not json", `{"Version":"latest"}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		a := &app{latestURL: srv.URL}
		if got := a.newerRelease(context.Background()); got != "" {
			t.Fatalf("body %q reported %q", body, got)
		}
		srv.Close()
	}
	a := &app{latestURL: "http://127.0.0.1:0/nothing-listens-here"}
	if got := a.newerRelease(context.Background()); got != "" {
		t.Fatalf("an unreachable proxy reported %q", got)
	}
}

// The version of the running binary belongs in the diagnosis whether or not the
// proxy answers: a bug report starts with it.
func TestDoctorAlwaysReportsTheRunningVersion(t *testing.T) {
	var out bytes.Buffer
	a := &app{
		cfg:     &config.Config{},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		runner: &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
			return execx.Result{}, errors.New("no docker here")
		}},
		out: &out,
	}
	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "version  ") {
		t.Fatalf("doctor should open on the version:\n%s", out.String())
	}
}
