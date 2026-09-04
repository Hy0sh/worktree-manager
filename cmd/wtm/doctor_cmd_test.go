package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// BackupConfig defaults to postgres even without a database, which is how a
// project with no dump came to be reported as running one.
func TestDoctorReportsAnEngineOnlyForProjectsWithADump(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{
		"withdump": {Dir: t.TempDir(), Dump: true},
		"nodump":   {Dir: t.TempDir()},
	}}
	// Docker and git answer nothing here: the table is the subject.
	a, _, out := newTestApp(t, cfg, "", func(execx.Cmd) (execx.Result, error) {
		return execx.Result{}, errors.New("unavailable")
	})

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
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
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
	})

	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"2 image(s) built for removed worktrees",
		"docker rmi my-app-wt-7-refactor-form-frontend my-app-wt-7-refactor-form-worker",
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
	cfg := &config.Config{Projects: map[string]config.Project{}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "dangling=true") && strings.Contains(c.String(), "com.docker.volume.anonymous") {
			return execx.Result{Stdout: "aaa111\nbbb222\nccc333\n"}, nil
		}
		return execx.Result{}, nil
	})
	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := out.String()
	for _, want := range []string{"3 anonymous volume(s)",
		"docker volume rm $(docker volume ls -q --filter dangling=true --filter label=com.docker.volume.anonymous)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor should say %q:\n%s", want, got)
		}
	}
}

// An index whose branch has no worktree any more is what a removal outside
// `wtm remove` leaves behind: it pushes every new worktree further out, and
// makes a foreign worktree on that branch look managed. doctor has to show it.
func TestDoctorReportsIndicesWithoutAWorktree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir,
		WorktreeIndices: map[string]int{"feat/live": 2, "feat/gone": 5, "worktree-curry": 7}}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n\n" +
				"worktree " + dir + "/.worktrees/feat/live\nbranch refs/heads/feat/live\n\n" +
				"worktree " + dir + "/.claude/worktrees/curry\nbranch refs/heads/worktree-curry\n"}, nil
		}
		return execx.Result{}, nil
	})
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

// The listing of what removed worktrees left behind is one command line per
// finding, seven of them on a busy machine: the reader has to be told there is
// a single verb for the lot.
func TestDoctorPointsAtCleanWhenSomethingWasLeftBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir,
		WorktreeIndices: map[string]int{"feat/gone": 5}}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n"}, nil
		}
		return execx.Result{}, nil
	})
	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "wtm clean") {
		t.Fatalf("doctor should name the verb that drops all of it:\n%s", out.String())
	}
}

func TestDoctorStaysSilentAboutCleanWithNothingToClean(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(execx.Cmd) (execx.Result, error) {
		return execx.Result{}, nil
	})
	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out.String(), "wtm clean") {
		t.Fatalf("nothing was left behind, so nothing to advertise:\n%s", out.String())
	}
}

// doctor scanned volume and image names and never a container label, so the
// one leftover that still holds RAM and ports went unmentioned, while the
// index allocator refused its index without saying who held it.
func TestDoctorReportsTheStacksOfRemovedWorktrees(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "ps -a") {
			return execx.Result{Stdout: "my-app-wt-4-old\nmy-app-wt-4-old\nmy-app\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	for _, want := range []string{
		"1 stack(s) of removed worktrees",
		"docker compose -p my-app-wt-4-old down --volumes",
		"`wtm clean` runs all of that in one go.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor should say %q:\n%s", want, out.String())
		}
	}
	// The main stack carries the repository name and no worktree prefix: its
	// containers are the developer's own.
	if strings.Contains(out.String(), "docker compose -p my-app down") {
		t.Fatalf("the main stack is not a worktree's:\n%s", out.String())
	}
}

// One worktree with no recorded index switches off the volume, image and stack
// reports for its whole project, and used to do it without a word: doctor
// answered "nothing left behind" where it meant "cannot tell".
func TestDoctorSaysWhyTheLeftoverReportsAreHeldBack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "worktree list"):
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n\n" +
				"worktree " + filepath.Join(dir, ".worktrees", "feat", "x") +
				"\nbranch refs/heads/feat/x\n"}, nil
		case strings.Contains(line, "volume ls"):
			return execx.Result{Stdout: "my-app-wt-9-gone_db-data\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	for _, want := range []string{"no recorded index", "myapp: feat/x"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor should say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "my-app-wt-9-gone_db-data") {
		t.Fatalf("the held-back report must stay held back:\n%s", out.String())
	}
}

// A project with no compose file starts no stack, so none of its worktrees
// ever gets an index. Counting them as unindexed would print the warning above
// forever, on every one of them.
func TestDoctorSaysNothingAboutIndicesAProjectWithoutAStackNeverHas(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n\n" +
				"worktree " + filepath.Join(dir, ".worktrees", "feat", "x") +
				"\nbranch refs/heads/feat/x\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newDoctorCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out.String(), "no recorded index") {
		t.Fatalf("a project without a compose file has no index to record:\n%s", out.String())
	}
}
