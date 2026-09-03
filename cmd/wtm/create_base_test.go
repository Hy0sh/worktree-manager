package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// baseFixture registers one project whose git carries two local branches and
// two remote ones, of which only a single remote branch has no local
// counterpart.
func baseFixture(t *testing.T) (*app, *execx.Fake) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, fake, _ := newTestApp(t, cfg, "", baseHandler(dir))
	return a, fake
}

// baseHandler answers the git a create walks through, for a repository at dir.
func baseHandler(dir string) func(execx.Cmd) (execx.Result, error) {
	return func(c execx.Cmd) (execx.Result, error) {
		// --show-toplevel is tested first: CurrentWorktree asks for it in the
		// same rev-parse that carries --git-common-dir.
		if strings.Contains(c.String(), "--show-toplevel") {
			return execx.Result{Stdout: strings.Join([]string{
				filepath.Join(dir, ".claude", "worktrees", "curry"),
				filepath.Join(dir, ".git", "worktrees", "w"),
				filepath.Join(dir, ".git"),
			}, "\n") + "\n"}, nil
		}
		if strings.Contains(c.String(), "--git-common-dir") {
			return execx.Result{Stdout: filepath.Join(dir, ".git") + "\n"}, nil
		}
		if strings.Contains(c.String(), "symbolic-ref") {
			return execx.Result{Stdout: "worktree-curry\n"}, nil
		}
		if strings.Contains(c.String(), "rev-parse --verify") {
			// No ref resolves: feat/new is the ordinary case of a branch being
			// created, and branchExists concludes on the exit code.
			return execx.Result{ExitCode: 1}, errors.New("unknown revision")
		}
		if strings.Contains(c.String(), "worktree add") {
			// dest is second to last in both forms git is called with, and the
			// steps after the checkout need it to exist.
			return execx.Result{}, os.MkdirAll(c.Args[len(c.Args)-2], 0o755)
		}
		if strings.Contains(c.String(), "for-each-ref") {
			return execx.Result{Stdout: strings.Join([]string{
				"refs/heads/develop", "refs/heads/feat/a",
				"refs/remotes/origin/HEAD", "refs/remotes/origin/develop",
				"refs/remotes/origin/release/1.4",
			}, "\n") + "\n"}, nil
		}
		return execx.Result{}, nil
	}
}

func TestCompleteCreateFollowsThePositions(t *testing.T) {
	a, _ := baseFixture(t)
	cmd := newCreateCmd(a)

	for _, tc := range []struct {
		name       string
		args       []string
		wantBases  bool
		wantFrom   bool
		wantNoFlag bool
	}{
		{name: "no argument yet: the projects", args: nil, wantNoFlag: true},
		{name: "project named: the branch is a new name", args: []string{"myapp"}, wantNoFlag: true},
		{name: "branch given: the base position", args: []string{"feat/new"},
			wantBases: true, wantFrom: true},
		{name: "project and branch: the base position too", args: []string{"myapp", "feat/new"},
			wantBases: true, wantFrom: true},
		{name: "base given: flags alone", args: []string{"feat/new", "develop"}},
		{name: "project, branch and base", args: []string{"myapp", "feat/new", "develop"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := a.completeCreate(cmd, tc.args, "")
			if slices.Contains(got, "develop") != tc.wantBases {
				t.Fatalf("bases offered = %v, want %v (got %v)", !tc.wantBases, tc.wantBases, got)
			}
			if slices.Contains(got, "--from-here") != tc.wantFrom {
				t.Fatalf("--from-here offered = %v, want %v (got %v)", !tc.wantFrom, tc.wantFrom, got)
			}
			hasFlag := slices.Contains(got, "--no-start")
			if hasFlag == tc.wantNoFlag {
				t.Fatalf("flags offered = %v (got %v)", hasFlag, got)
			}
		})
	}
	if got, _ := a.completeCreate(cmd, nil, ""); !slices.Contains(got, "myapp") {
		t.Fatalf("the first position offers the projects, got %v", got)
	}
}

// A base is any branch, which is not the question worktreeBranches answers.
func TestBaseBranchesTakeLocalsAndTheRemotesTheyDoNotStandFor(t *testing.T) {
	a, _ := baseFixture(t)
	got := a.baseBranches("myapp")
	for _, want := range []string{"develop", "feat/a", "origin/release/1.4"} {
		if !slices.Contains(got, want) {
			t.Fatalf("%q missing from %v", want, got)
		}
	}
	for _, unwanted := range []string{"origin/HEAD", "origin/develop"} {
		if slices.Contains(got, unwanted) {
			t.Fatalf("%q should not be offered: %v", unwanted, got)
		}
	}
}

func TestCreateRefusesFromHereWithAPositionalBase(t *testing.T) {
	a, _ := baseFixture(t)
	cmd := newCreateCmd(a)
	cmd.SetArgs([]string{"myapp", "feat/new", "develop", "--from-here"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "both say where to cut from") {
		t.Fatalf("expected a refusal naming the conflict, got %v", err)
	}
}

// The base is applied with -C on the main repository, so it has to be resolved
// in the current directory first and passed by name: from a worktree, the
// project's base is never the branch the caller is standing on.
func TestCreateFromHereCutsFromTheCurrentBranch(t *testing.T) {
	a, fake := baseFixture(t)
	cmd := newCreateCmd(a)
	cmd.SetArgs([]string{"myapp", "feat/new", "--from-here"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create --from-here: %v", err)
	}
	var add string
	for _, l := range fake.Lines() {
		if strings.Contains(l, "worktree add") {
			add = l
		}
	}
	if !strings.HasSuffix(add, " worktree-curry") {
		t.Fatalf("the worktree must be cut from the current branch, got %q", add)
	}
}
