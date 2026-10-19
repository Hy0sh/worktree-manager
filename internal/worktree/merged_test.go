package worktree

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// history stands for the repository: which refs hold the worktree's head, and
// which of them have it on their first-parent line, where a fast-forward or a
// branch that never diverged leaves it and a merge commit does not.
type history struct {
	status      string
	upstream    string
	ancestorOf  []string
	firstParent []string
	ahead       string
}

func (h history) handler(c execx.Cmd) (execx.Result, error) {
	args := c.Args
	switch {
	case slices.Contains(args, "status"):
		return execx.Result{Stdout: h.status}, nil
	case slices.Contains(args, "--abbrev-ref"):
		if h.upstream == "" {
			return execx.Result{ExitCode: 128}, errors.New("no upstream configured")
		}
		return execx.Result{Stdout: h.upstream + "\n"}, nil
	case slices.Contains(args, "--is-ancestor"):
		if slices.Contains(h.ancestorOf, args[len(args)-1]) {
			return execx.Result{}, nil
		}
		return execx.Result{ExitCode: 1}, errors.New("exit status 1")
	case slices.Contains(args, "--first-parent"):
		ref := args[slices.Index(args, "--first-parent")+1]
		out := "fff\neee\n"
		if slices.Contains(h.firstParent, ref) {
			out += "aaa\n"
		}
		return execx.Result{Stdout: out}, nil
	case slices.Contains(args, "--count"):
		if h.ahead == "" {
			return execx.Result{ExitCode: 128}, errors.New("unknown revision develop")
		}
		return execx.Result{Stdout: h.ahead + "\n"}, nil
	}
	return execx.Result{}, errors.New("unexpected: " + c.String())
}

func TestInspectMerge(t *testing.T) {
	branch := stack.Worktree{Path: "/repo/.worktrees/feat/a", Branch: "feat/a", Head: "aaa", UnderRoot: true}
	for _, tc := range []struct {
		name string
		wt   stack.Worktree
		h    history
		safe bool
		want string
	}{
		{"merge commit", branch, history{ancestorOf: []string{"develop"}, ahead: "0"},
			true, "merged into develop"},
		{"tracked change", branch, history{status: " M app.py\n", ancestorOf: []string{"develop"}},
			false, "uncommitted or untracked files (app.py)"},
		// remove takes untracked files without a word, and git add never saw them.
		{"untracked file", branch, history{status: "?? notes.md\n", ancestorOf: []string{"develop"}},
			false, "uncommitted or untracked files (notes.md)"},
		// develop by default, in a repository that has none: no count is no zero.
		{"base missing", branch, history{}, false, "cannot compare with develop"},
		{"unique commits", branch, history{ahead: "2"}, false, "2 commit(s) not in develop"},
		// A worktree created a minute ago: nothing unique, and still somebody's.
		{"fresh branch", branch, history{ancestorOf: []string{"develop"}, firstParent: []string{"develop"}, ahead: "0"},
			false, "on develop's own line"},
		// Indistinguishable from the fresh one, so kept for the same reason.
		{"fast-forward", branch, history{ancestorOf: []string{"develop"}, firstParent: []string{"develop"}, ahead: "0"},
			false, "on develop's own line"},
		// Their commits were rewritten, so the head is in no base: not detected.
		{"squash or rebase", branch, history{ahead: "3"}, false, "3 commit(s) not in develop"},
		{"merged on the remote, local base behind", branch,
			history{upstream: "origin/develop", ancestorOf: []string{"origin/develop"}, ahead: "1"},
			true, "merged into origin/develop"},
		{"no upstream, local base behind", branch, history{ahead: "1"}, false, "1 commit(s) not in develop"},
		{"switched outside wtm", stack.Worktree{Branch: "feat/a", Holds: "feat/b", Head: "aaa"},
			history{ancestorOf: []string{"develop"}}, false, "switched to feat/b"},
		{"detached", stack.Worktree{Branch: "feat/a", Detached: true, Head: "aaa"},
			history{ancestorOf: []string{"develop"}}, false, "detached HEAD"},
		{"locked", stack.Worktree{Branch: "feat/a", Locked: true, Head: "aaa"},
			history{ancestorOf: []string{"develop"}}, false, "locked"},
		{"the base", stack.Worktree{Branch: "develop", Head: "aaa"},
			history{ancestorOf: []string{"develop"}}, false, "it is the base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := Options{Base: "develop", Project: config.Project{Dir: "/repo"},
				Runner: &execx.Fake{Handler: tc.h.handler}}
			v := InspectMerge(context.Background(), o, tc.wt)
			if v.Safe != tc.safe || !strings.Contains(v.Reason, tc.want) {
				t.Fatalf("got %+v, want safe=%v with %q", v, tc.safe, tc.want)
			}
		})
	}
}
