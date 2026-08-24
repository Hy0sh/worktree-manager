package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// A blank BRANCH column reads as a bug in wtm rather than as what it is: a
// worktree whose HEAD was moved off its branch, by a `git checkout <rev>` there.
func TestListNamesTheBranchAndTheDetachedHead(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	a := &app{
		cfg: &config.Config{Projects: map[string]config.Project{
			"myapp": {Dir: dir},
		}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		runner: &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
			if strings.Contains(c.String(), "worktree list") {
				return execx.Result{Stdout: "worktree " + dir + "\nHEAD abc\nbranch refs/heads/main\n\n" +
					"worktree " + filepath.Join(dir, ".worktrees", "refactor/x") +
					"\nHEAD 37a276b48e772823\ndetached\n"}, nil
			}
			return execx.Result{}, nil
		}},
		out: &out,
	}

	cmd := newListCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("list: %v", err)
	}

	if !strings.Contains(out.String(), "refactor/x (detached 37a276b4)") {
		t.Fatalf("the branch of the path and where HEAD sits must both show:\n%s", out.String())
	}
}
