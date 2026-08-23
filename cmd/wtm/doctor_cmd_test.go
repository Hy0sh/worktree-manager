package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// BackupConfig defaults to postgres even without a database, which is how a
// project with no dump came to be reported as running one.
func TestDoctorReportsAnEngineOnlyForProjectsWithADump(t *testing.T) {
	var out bytes.Buffer
	a := &app{
		cfg: &config.Config{Projects: map[string]config.Project{
			"withdump": {Dir: t.TempDir(), Dump: true},
			"nodump":   {Dir: t.TempDir()},
		}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		// Docker and git answer nothing here: the table is the subject.
		runner: &execx.Fake{Handler: func(execx.Cmd) (execx.Result, error) {
			return execx.Result{}, errors.New("unavailable")
		}},
		out: &out,
	}

	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "nodump":
			if last := fields[len(fields)-1]; last != "-" {
				t.Fatalf("a project without a dump must report no engine, got %q in %q", last, line)
			}
		case "withdump":
			if last := fields[len(fields)-1]; last != "postgres" {
				t.Fatalf("engine = %q in %q", last, line)
			}
		}
	}
	if !strings.Contains(out.String(), "nodump") {
		t.Fatalf("both projects should be listed:\n%s", out.String())
	}
}
