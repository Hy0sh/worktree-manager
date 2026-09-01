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
	dir := filepath.Join(t.TempDir(), "my-app")
	var out bytes.Buffer
	a := &app{
		cfg:     &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		runner: &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
			switch line := c.String(); {
			case strings.Contains(line, "images --format"):
				return execx.Result{Stdout: "my-app-wt-7-refactor-form-frontend\n" +
					"my-app-wt-7-refactor-form-worker\n" +
					"my-app\n" + // the main stack's own image, not a worktree's
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
		"docker rmi my-app-wt-7-refactor-form-worker my-app-wt-7-refactor-form-frontend",
		"57.8GB of build cache",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor should say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n  postgres\n") || strings.Contains(got, "\n  my-app\n") {
		t.Fatalf("only worktree images belong in the report:\n%s", got)
	}
}

// Anonymous volumes carry no compose label, so the orphan report built on that
// label never sees them, and a project without a `volumes:` section leaks one
// per start. doctor can at least count what docker holds that nothing mounts.
func TestDoctorCountsDanglingAnonymousVolumes(t *testing.T) {
	var out bytes.Buffer
	a := &app{
		cfg:     &config.Config{Projects: map[string]config.Project{}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		runner: &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
			if strings.Contains(c.String(), "dangling=true") && strings.Contains(c.String(), "com.docker.volume.anonymous") {
				return execx.Result{Stdout: "aaa111\nbbb222\nccc333\n"}, nil
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
	for _, want := range []string{"3 anonymous volume(s)", "docker volume rm aaa111 bbb222 ccc333"} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor should say %q:\n%s", want, got)
		}
	}
}

// An index whose branch has no worktree any more is what a removal outside
// `wtm remove` leaves behind. It pushes every new worktree further out, and
// with adoption keyed on recorded indices it also makes a foreign worktree on
// that branch look managed. doctor is where it has to show.
func TestDoctorReportsIndicesWithoutAWorktree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	var out bytes.Buffer
	a := &app{
		cfg: &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir,
			WorktreeIndices: map[string]int{"feat/live": 2, "feat/gone": 5, "worktree-curry": 7}}}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		runner: &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
			if strings.Contains(c.String(), "worktree list") {
				return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n\n" +
					"worktree " + dir + "/.worktrees/feat/live\nbranch refs/heads/feat/live\n\n" +
					"worktree " + dir + "/.claude/worktrees/curry\nbranch refs/heads/worktree-curry\n"}, nil
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
	if !strings.Contains(got, "myapp: index 5 is recorded for feat/gone, which has no worktree") {
		t.Fatalf("the stale index must be named:\n%s", got)
	}
	if !strings.Contains(got, "wtm remove myapp feat/gone") {
		t.Fatalf("the release command must be named:\n%s", got)
	}
	// worktree-curry is adopted: recorded, and its worktree exists outside .worktrees.
	if strings.Contains(got, "worktree-curry") {
		t.Fatalf("an adopted worktree is not stale:\n%s", got)
	}
}
