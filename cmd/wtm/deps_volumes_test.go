package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

// depsFixture registers one project whose main checkout and one live worktree
// each carry a lockfile, and a docker holding a volume per key plus a stale one.
func depsFixture(t *testing.T, listFails bool) (*config.Config, func(execx.Cmd) (execx.Result, error)) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my-app")
	live := filepath.Join(dir, ".worktrees", "feat", "live")
	for path, content := range map[string]string{dir: "main", live: "live"} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "go.sum"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mainKey, liveKey := worktree.DepsKey(dir), worktree.DepsKey(live)
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	return cfg, func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "worktree list"):
			if listFails {
				return execx.Result{}, errors.New("git unavailable")
			}
			return execx.Result{Stdout: "worktree " + dir + "\nbranch refs/heads/develop\n\n" +
				"worktree " + live + "\nbranch refs/heads/feat/live\n"}, nil
		case strings.Contains(line, "label=wtm.deps-key"):
			return execx.Result{Stdout: "deps-main\t" + mainKey + "\ndeps-live\t" + liveKey +
				"\ndeps-gone\tdeadbeef0000\n"}, nil
		}
		return execx.Result{}, nil
	}
}

func TestOrphanDepsVolumesKeepsWhatAWorktreeOrTheMainCheckoutUses(t *testing.T) {
	cfg, run := depsFixture(t, false)
	a, _, _ := newTestApp(t, cfg, "", run)

	got := a.orphanDepsVolumes(t.Context(), a.liveProjects(t.Context(), a.cfg.Names()))
	if len(got) != 1 || got[0] != "deps-gone" {
		t.Fatalf("only the volume of an unused lockfile is orphan, got %v", got)
	}
}

func TestOrphanDepsVolumesHoldBackWhenAProjectCannotBeListed(t *testing.T) {
	cfg, run := depsFixture(t, true)
	a, _, _ := newTestApp(t, cfg, "", run)

	if got := a.orphanDepsVolumes(t.Context(), a.liveProjects(t.Context(), a.cfg.Names())); got != nil {
		t.Fatalf("a project that failed to list would lose its live volumes, got %v", got)
	}
}

func TestDoctorReportsAndCleanDropsDepsVolumesOfAnUnusedLockfile(t *testing.T) {
	cfg, run := depsFixture(t, false)
	a, fake, out := newTestApp(t, cfg, "y\n", run)

	doctor := newDoctorCmd(a)
	if err := doctor.RunE(doctor, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "1 dependency volume(s) of a lockfile no worktree uses any more:") ||
		!strings.Contains(out.String(), "docker volume rm deps-gone") ||
		!strings.Contains(out.String(), "`wtm clean` runs all of that") {
		t.Fatalf("doctor should report the orphan and point at clean:\n%s", out.String())
	}

	clean := newCleanCmd(a)
	if err := clean.RunE(clean, nil); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if lines := strings.Join(fake.Lines(), "\n"); !strings.Contains(lines, "docker volume rm deps-gone") ||
		strings.Contains(lines, "rm deps-main") || strings.Contains(lines, "deps-live") {
		t.Fatalf("clean should drop exactly the orphan:\n%s", lines)
	}
}

func TestScopedCleanLeavesDepsVolumesAlone(t *testing.T) {
	cfg, run := depsFixture(t, false)
	a, fake, _ := newTestApp(t, cfg, "y\n", run)

	clean := newCleanCmd(a)
	if err := clean.RunE(clean, []string{"myapp"}); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if lines := strings.Join(fake.Lines(), "\n"); strings.Contains(lines, "deps-gone") {
		t.Fatalf("a scoped clean must not touch dependency volumes:\n%s", lines)
	}
}
