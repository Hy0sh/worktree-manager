package wtc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

const porcelain = `worktree /repo/myapp
HEAD abc123
branch refs/heads/develop

worktree /repo/myapp/.worktrees/feat-a
HEAD def456
branch refs/heads/feat/a

worktree /repo/myapp/.worktrees/feat-b
HEAD 789abc
branch refs/heads/feat/b
`

func newClient(t *testing.T, f *execx.Fake) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	return &Client{Runner: f, Dir: dir, Out: io.Discard}, dir
}

func installBin(t *testing.T, dir string) {
	t.Helper()
	binDir := filepath.Join(dir, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "wtc"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureAvailableMissingBinaryIsActionable(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no global wtc either
	c, _ := newClient(t, &execx.Fake{})
	err := c.EnsureAvailable()
	if err == nil {
		t.Fatal("expected an error")
	}
	// Both installation routes should be offered: local to the project, or
	// global for projects that have no business carrying a Node dependency.
	for _, want := range []string{"node_modules", "npm install -g worktree-compose", "--no-start"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}
}

// A PHP or Python project should not need a package.json just to get wtc.
func TestResolveFallsBackToAGlobalInstall(t *testing.T) {
	globalDir := t.TempDir()
	global := filepath.Join(globalDir, "wtc")
	if err := os.WriteFile(global, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", globalDir)

	c, _ := newClient(t, &execx.Fake{})
	got, err := c.Locate()
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got.Path != global {
		t.Fatalf("resolved %q, want the global binary %q", got.Path, global)
	}
	if got.Origin != OriginGlobal {
		t.Fatalf("origin = %q, want %q", got.Origin, OriginGlobal)
	}
}

// `wtc --version` reports a hardcoded 0.1.0 even when the installed package is
// 0.2.0, so the version has to come from package.json.
func TestLocateReadsTheRealVersionFromPackageJSON(t *testing.T) {
	c, dir := newClient(t, &execx.Fake{})
	pkgDir := filepath.Join(dir, "node_modules", "worktree-compose")
	if err := os.MkdirAll(filepath.Join(pkgDir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"worktree-compose","version":"0.2.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(pkgDir, "dist", "cli.js")
	if err := os.WriteFile(cli, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// npm installs the binary as a symlink into the package, like the real thing
	if err := os.Symlink(cli, filepath.Join(binDir, "wtc")); err != nil {
		t.Fatal(err)
	}

	got, err := c.Locate()
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got.Version != "0.2.0" {
		t.Fatalf("version = %q, want 0.2.0", got.Version)
	}
	if got.Origin != OriginProject {
		t.Fatalf("origin = %q, want %q", got.Origin, OriginProject)
	}
}

func TestResolvePrefersTheProjectInstallOverTheGlobalOne(t *testing.T) {
	globalDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(globalDir, "wtc"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", globalDir)

	c, dir := newClient(t, &execx.Fake{})
	installBin(t, dir)
	got, err := c.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(dir, "node_modules", ".bin", "wtc"); got != want {
		t.Fatalf("resolved %q, want the project-pinned binary %q", got, want)
	}
}

func TestResolveHonoursAnExplicitPath(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "wtc-custom")
	if err := os.WriteFile(custom, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, dir := newClient(t, &execx.Fake{})
	installBin(t, dir) // even with a project install, the explicit path wins
	c.Bin = custom
	got, err := c.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != custom {
		t.Fatalf("resolved %q, want %q", got, custom)
	}

	c.Bin = filepath.Join(t.TempDir(), "nowhere")
	if _, err := c.Resolve(); err == nil {
		t.Fatal("a configured path that does not exist should be an error")
	}
}

func TestWorktreesSkipsMainAndIndexesFromOne(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: porcelain}, nil
	}}
	c, _ := newClient(t, f)
	wts, err := c.Worktrees(context.Background())
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("expected 2 non-main worktrees, got %d", len(wts))
	}
	if wts[0].Index != 1 || wts[0].Branch != "feat/a" {
		t.Fatalf("first worktree = %+v", wts[0])
	}
	if wts[1].Index != 2 || wts[1].Path != "/repo/myapp/.worktrees/feat-b" {
		t.Fatalf("second worktree = %+v", wts[1])
	}
	if got := f.Lines()[0]; !strings.Contains(got, "worktree list --porcelain") {
		t.Fatalf("expected a porcelain listing, got %q", got)
	}
}

func TestFindByBranch(t *testing.T) {
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: porcelain}, nil
	}}
	c, _ := newClient(t, f)
	wt, err := c.FindByBranch(context.Background(), "feat/b")
	if err != nil {
		t.Fatalf("FindByBranch: %v", err)
	}
	if wt.Index != 2 {
		t.Fatalf("index = %d, want 2", wt.Index)
	}
	if _, err := c.FindByBranch(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for an unknown branch")
	} else if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should mention the branch, got %q", err.Error())
	}
}

func TestStartAndStopInvokeBinaryWithIndex(t *testing.T) {
	f := &execx.Fake{}
	c, dir := newClient(t, f)
	installBin(t, dir)

	if err := c.Start(context.Background(), 2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Stop(context.Background(), 3); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []string{
		filepath.Join(dir, "node_modules", ".bin", "wtc") + " start 2",
		filepath.Join(dir, "node_modules", ".bin", "wtc") + " stop 3",
	}
	got := f.Lines()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if f.Calls[0].Dir != dir {
		t.Fatalf("wtc must run with the project as cwd, got %q", f.Calls[0].Dir)
	}
}

func TestReadPortsParsesOverrideBlock(t *testing.T) {
	wtDir := t.TempDir()
	env := "FOO=bar\n\n# --- wtc port overrides ---\nBACKEND_PORT=28007\nDB_PORT=25439\n# --- end wtc ---\n"
	if err := os.WriteFile(filepath.Join(wtDir, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	ports, err := ReadPorts(wtDir)
	if err != nil {
		t.Fatalf("ReadPorts: %v", err)
	}
	if len(ports) != 2 || ports[0] != "BACKEND_PORT=28007" || ports[1] != "DB_PORT=25439" {
		t.Fatalf("ports = %v", ports)
	}
}

func TestReadPortsWithoutBlockReturnsNothing(t *testing.T) {
	wtDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wtDir, ".env"), []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ports, err := ReadPorts(wtDir)
	if err != nil {
		t.Fatalf("ReadPorts: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected no ports, got %v", ports)
	}
}

// The compose project name is the only handle on a worktree stack's containers
// and volumes. This is the exact name wtc produced for branch feat/wtm-e2e.
func TestProjectNameMatchesWtc(t *testing.T) {
	if got := ProjectName("gallia-utopia", 1, "feat/wtm-e2e"); got != "gallia-utopia-wt-1-feat-wtm-e2e" {
		t.Fatalf("ProjectName = %q", got)
	}
	if got := ProjectName("my-app", 3, "Fix/GAL_42--Weird"); got != "my-app-wt-3-fix-gal-42-weird" {
		t.Fatalf("ProjectName = %q", got)
	}
}
