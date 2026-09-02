package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestCleanRemovesNothingWhenNothingWasLeftBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir,
		WorktreeIndices: map[string]int{"feat/live": 2}}}}
	a, fake, out := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n\n" +
				"worktree " + dir + "/.worktrees/feat/live\nbranch refs/heads/feat/live\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newCleanCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("clean: %v", err)
	}

	if !strings.Contains(out.String(), "nothing to clean") {
		t.Fatalf("clean should say there is nothing to do:\n%s", out.String())
	}
	for _, line := range fake.Lines() {
		if strings.Contains(line, "volume rm") || strings.Contains(line, "rmi") {
			t.Fatalf("nothing must be dropped: %s", line)
		}
	}
}

// The volumes and images of a stale index go with the index: releasing it runs
// a `compose down` and the labelled sweeps. Dropping a list scanned before that
// would re-issue a `docker volume rm` on names docker no longer holds, and
// report the failure as if the cleanup had gone wrong.
func TestCleanRescansDockerAfterReleasingTheIndices(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir,
		WorktreeIndices: map[string]int{"feat/gone": 5}}}}

	// Each kind disappears from docker the moment it is dropped, which is what
	// makes a second scan tell a different story from the first.
	volumesSwept, imagesSwept := false, false
	a, fake, out := newTestApp(t, cfg, "y\n", func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "worktree list"):
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n"}, nil
		case strings.Contains(line, "volume rm"):
			volumesSwept = true
		case strings.Contains(line, "rmi"):
			imagesSwept = true
		case strings.Contains(line, "volume ls"):
			if !volumesSwept {
				return execx.Result{Stdout: "my-app-wt-5-feat-gone_db-data\n"}, nil
			}
		case strings.Contains(line, "images"):
			if !imagesSwept {
				return execx.Result{Stdout: "my-app-wt-5-feat-gone-backend\n"}, nil
			}
		}
		return execx.Result{}, nil
	})

	cmd := newCleanCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("clean: %v", err)
	}

	if !strings.Contains(out.String(), "index 5 released") {
		t.Fatalf("the stale index must be released:\n%s", out.String())
	}
	for _, verb := range []string{"volume rm", "rmi"} {
		n := 0
		for _, line := range fake.Lines() {
			if strings.Contains(line, verb) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("`docker %s` ran %d times, the release already swept what it dropped:\n%s",
				verb, n, strings.Join(fake.Lines(), "\n"))
		}
	}
}

func TestCleanRemovesNothingWhenTheAnswerIsNo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir,
		WorktreeIndices: map[string]int{"feat/gone": 5}}}}
	a, fake, _ := newTestApp(t, cfg, "n\n", func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "worktree list"):
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n"}, nil
		case strings.Contains(line, "volume ls"):
			return execx.Result{Stdout: "my-app-wt-5-feat-gone_db-data\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newCleanCmd(a)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("a refused confirmation must cancel, got %v", err)
	}
	for _, line := range fake.Lines() {
		if strings.Contains(line, "volume rm") || strings.Contains(line, "rmi") || strings.Contains(line, "compose") {
			t.Fatalf("nothing must be touched after a no: %s", line)
		}
	}
}

// Index 9 was released long ago and only docker still names it, so releasing
// indices cannot reach what it left: the sweep by name is the only way out.
func TestCleanDropsLeftoversNoRecordedIndexPointsAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir,
		WorktreeIndices: map[string]int{"feat/live": 2}}}}
	a, fake, _ := newTestApp(t, cfg, "y\n", func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "worktree list"):
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n\n" +
				"worktree " + dir + "/.worktrees/feat/live\nbranch refs/heads/feat/live\n"}, nil
		case strings.Contains(line, "volume ls"):
			return execx.Result{Stdout: "my-app-wt-9-old_db-data\nmy-app-wt-2-feat-live_db-data\n"}, nil
		case strings.Contains(line, "images --format"):
			return execx.Result{Stdout: "my-app-wt-9-old-backend\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newCleanCmd(a)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("clean: %v", err)
	}

	lines := strings.Join(fake.Lines(), "\n")
	for _, want := range []string{
		"docker volume rm my-app-wt-9-old_db-data",
		"docker rmi my-app-wt-9-old-backend",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("clean should run %q:\n%s", want, lines)
		}
	}
	if strings.Contains(lines, "feat-live_db-data") {
		t.Fatalf("the volume of a live worktree must be left alone:\n%s", lines)
	}
}

func TestCleanOnOneProjectLeavesTheOtherAlone(t *testing.T) {
	root := t.TempDir()
	mine, other := filepath.Join(root, "my-app"), filepath.Join(root, "happy-shop")
	cfg := &config.Config{Projects: map[string]config.Project{
		"myapp": {Dir: mine},
		"shop":  {Dir: other},
	}}
	a, fake, _ := newTestApp(t, cfg, "y\n", func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "worktree list"):
			return execx.Result{}, nil
		case strings.Contains(line, "volume ls"):
			return execx.Result{Stdout: "my-app-wt-9-old_db-data\nhappy-shop-wt-3-old_db-data\n"}, nil
		}
		return execx.Result{}, nil
	})

	cmd := newCleanCmd(a)
	if err := cmd.RunE(cmd, []string{"myapp"}); err != nil {
		t.Fatalf("clean: %v", err)
	}

	lines := strings.Join(fake.Lines(), "\n")
	if !strings.Contains(lines, "docker volume rm my-app-wt-9-old_db-data") {
		t.Fatalf("the named project must be cleaned:\n%s", lines)
	}
	if strings.Contains(lines, "happy-shop") {
		t.Fatalf("a project nobody named must not be touched:\n%s", lines)
	}
}
