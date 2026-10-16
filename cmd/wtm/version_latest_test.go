package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
		// `go install ...@main`, or @latest on an untagged commit, stamps the
		// pseudo-version the toolchain derives from the commit it built. It was
		// not comparable at all, so such a build was never told it was behind.
		{name: "installed from a commit", local: "v0.8.1-0.20260828143234-4c0fbbb36bed",
			answer: "v0.9.0", reported: true},
		// Semver puts a pre-release before the release it leads to, and that is
		// exactly what this shape means: after v0.8.0, before v0.8.1.
		{name: "commit ahead of the last tag, behind the next",
			local: "v0.8.1-0.20260828143234-4c0fbbb36bed", answer: "v0.8.1", reported: true},
		{name: "commit past the published tag", local: "v0.9.1-0.20260828143234-4c0fbbb36bed",
			answer: "v0.9.0"},
		// Printed as is by doctor: only a bare release tag is worth a line.
		{name: "escape codes in a pre-release", local: "v0.4.7", answer: "v0.4.8-\x1b]0;x\a"},
		{name: "escape codes in build metadata", local: "v0.4.7", answer: "v0.4.8+\x1b[2J"},
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

// A copy of wtm ahead of GOPATH/bin on the PATH made `go install` exit 0 and
// change nothing; a link to the installed binary is the healthy setup.
func TestInstalledElsewhereSpotsACopyOutsideTheInstallDir(t *testing.T) {
	gopath, other := t.TempDir(), t.TempDir()
	bin := filepath.Join(gopath, "bin")
	installed := filepath.Join(bin, "wtm")
	copied := filepath.Join(other, "wtm")
	linked := filepath.Join(other, "wtm-link")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{installed, copied} {
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(installed, linked); err != nil {
		t.Fatal(err)
	}
	goEnv := func(stdout string, err error) *app {
		return &app{runner: &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
			return execx.Result{Stdout: stdout}, err
		}}}
	}

	for _, tc := range []struct {
		name string
		a    *app
		exe  string
		want string
	}{
		{"copy", goEnv("\n"+gopath+"\n", nil), copied, bin},
		{"link", goEnv("\n"+gopath+"\n", nil), linked, ""},
		{"installed", goEnv("\n"+gopath+"\n", nil), installed, ""},
		{"GOBIN wins", goEnv(other+"\n"+gopath+"\n", nil), copied, ""},
		{"GOPATH list", goEnv("\n"+gopath+string(filepath.ListSeparator)+other+"\n", nil), copied, bin},
		{"no go", goEnv("", errors.New("go is not installed")), copied, ""},
	} {
		if got := tc.a.installedElsewhere(context.Background(), tc.exe); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
