package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// newTestApp holds the registry both in memory and on disk, since config.WithLock
// reads the file back: a command that writes to it needs the two to agree. The
// buffer is returned as well as wired, for the tests reading what was printed.
func newTestApp(t *testing.T, cfg *config.Config, in string,
	handler func(execx.Cmd) (execx.Result, error)) (*app, *execx.Fake, *bytes.Buffer) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &execx.Fake{Handler: handler}
	out := &bytes.Buffer{}
	return &app{cfg: cfg, cfgPath: cfgPath, backups: t.TempDir(),
		runner: fake, out: out, in: strings.NewReader(in)}, fake, out
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
