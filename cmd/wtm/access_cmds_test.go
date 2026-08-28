package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// `wtm path` exists to be substituted: `cd $(wtm path feat/x)`. Whatever
// diagnosis a detached worktree deserves elsewhere, a second line here lands in
// the shell's argument.
func TestPathPrintsNothingButThePath(t *testing.T) {
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

	cmd := newPathCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp", "refactor/x"}); err != nil {
		t.Fatalf("path: %v", err)
	}

	want := filepath.Join(dir, ".worktrees", "refactor/x") + "\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

// wtm exists for parallel agents: a `create` reaching a memory warning must
// never hang on a question nobody will answer, nor fail on one nobody read.
// A terminal is the only evidence that somebody is there.
func TestNothingIsAskedWithoutATerminal(t *testing.T) {
	a := &app{in: strings.NewReader("y\n"), out: &bytes.Buffer{}}
	if a.confirmer() != nil {
		t.Fatal("a piped stdin is a script, and a script is never asked")
	}
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a.in = f
	if a.confirmer() != nil {
		t.Fatal("/dev/null is not a terminal either")
	}
}
