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

// The images a worktree stack builds outlived every removal until wtm learned
// to drop them: on one machine, 153 of them for 40 worktrees long gone.
func TestDoctorReportsOrphanImagesAndBuildCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallia-utopia")
	var out bytes.Buffer
	a := &app{
		cfg:     &config.Config{Projects: map[string]config.Project{"gallia": {Dir: dir}}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		runner: &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
			switch line := c.String(); {
			case strings.Contains(line, "images --format"):
				return execx.Result{Stdout: "gallia-utopia-wt-7-refactor-form-frontend\n" +
					"gallia-utopia-wt-7-refactor-form-celery_worker\n" +
					"gallia-utopia\n" + // the main stack's own image, not a worktree's
					"postgres\n"}, nil
			case strings.Contains(line, "buildx du"):
				return execx.Result{Stdout: "Private:\t19.9GB\nReclaimable:\t57.8GB\nTotal:\t57.8GB\n"}, nil
			}
			return execx.Result{}, nil
		}},
		out: &out,
	}

	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"2 image(s) built for removed worktrees",
		"docker rmi gallia-utopia-wt-7-refactor-form-celery_worker gallia-utopia-wt-7-refactor-form-frontend",
		"57.8GB of build cache",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor should say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n  postgres\n") || strings.Contains(got, "\n  gallia-utopia\n") {
		t.Fatalf("only worktree images belong in the report:\n%s", got)
	}
}
