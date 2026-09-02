package main

import (
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// Measured on two real projects: one's db_test (offset 1000, default 5433) and
// the other's auth service (offset 2000, default 4433) both land on 26434, because
// the offset step is smaller than the spread of the default ports it shifts.
func TestPortClashesReportsTwoProjectsOnTheSamePort(t *testing.T) {
	lines := portClashes([]portHolder{
		{Port: 26434, Project: "alpha", Branch: "feat/x", Label: "db_test:5432"},
		{Port: 26434, Project: "beta", Branch: "feat/y", Label: "auth:4433"},
		{Port: 26433, Project: "alpha", Branch: "feat/x", Label: "db:5432"},
	})
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want the single clash", lines)
	}
	for _, want := range []string{"26434", "alpha/feat/x", "beta/feat/y"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line should mention %q, got %q", want, lines[0])
		}
	}
}

// Two branches of one project never clash: the allocator gives each index its
// own port, and refuses within a project anyway. Reporting them would be noise.
func TestPortClashesIgnoresOneProjectWithItself(t *testing.T) {
	lines := portClashes([]portHolder{
		{Port: 26434, Project: "alpha", Branch: "feat/x", Label: "db:5432"},
		{Port: 26434, Project: "alpha", Branch: "feat/y", Label: "db:5432"},
	})
	if len(lines) != 0 {
		t.Fatalf("lines = %v, want none", lines)
	}
}

func TestOrphanVolumesKeepsWhatALiveWorktreeOwns(t *testing.T) {
	all := []string{
		"my-app-wt-1-feat-x_postgres_data",
		"my-app-wt-2-fix-migration_postgres_data",
		"my-app-wt-2-fix-migration_files_data",
		"other-project-wt-1-feat-x_data", // another repository's, not ours to report
		"my-app_postgres_data",           // the main stack, not a worktree
	}
	rw := repoWorktrees{Repo: "my-app", Live: []string{"my-app-wt-1-feat-x"}}
	orphans := rw.orphanVolumes(all)
	want := []string{
		"my-app-wt-2-fix-migration_files_data",
		"my-app-wt-2-fix-migration_postgres_data",
	}
	if len(orphans) != len(want) {
		t.Fatalf("orphans = %v, want %v", orphans, want)
	}
	for i := range want {
		if orphans[i] != want[i] {
			t.Fatalf("orphans = %v, want %v", orphans, want)
		}
	}
}

// With no live worktree recorded, everything under the prefix is orphan: that
// is exactly the state a project left after its worktrees were removed.
func TestOrphanVolumesReportsAllWhenNothingIsLive(t *testing.T) {
	orphans := repoWorktrees{Repo: "webshop"}.orphanVolumes([]string{"webshop-wt-1-feat-x_pgdata", "webshop_pgdata"})
	if len(orphans) != 1 || orphans[0] != "webshop-wt-1-feat-x_pgdata" {
		t.Fatalf("orphans = %v", orphans)
	}
}

func TestOrphanImagesKeepsWhatALiveWorktreeOwns(t *testing.T) {
	all := []string{
		"my-app-wt-1-feat-x-frontend",
		"my-app-wt-2-fix-migration-frontend",
		"my-app-wt-2-fix-migration-worker",
		"other-project-wt-1-feat-x-api", // another repository's, not ours to report
		"my-app-frontend",               // the main stack, not a worktree
		"postgres",
	}
	rw := repoWorktrees{Repo: "my-app", Live: []string{"my-app-wt-1-feat-x"}}
	orphans := rw.orphanImages(all)
	want := []string{
		"my-app-wt-2-fix-migration-frontend",
		"my-app-wt-2-fix-migration-worker",
	}
	if len(orphans) != len(want) {
		t.Fatalf("orphans = %v, want %v", orphans, want)
	}
	for i := range want {
		if orphans[i] != want[i] {
			t.Fatalf("orphans = %v, want %v", orphans, want)
		}
	}
}

// Compose sanitises the project name it builds from the repository directory,
// so a directory not already lowercase and alphanumeric never matched the prefix
// doctor looked for: it reported zero orphan, silently.
func TestOrphanVolumesMatchTheNameComposeActuallyBuilds(t *testing.T) {
	all := []string{"myapp-wt-1-feat-x_db_data", "myapp-wt-2-old_db_data", "unrelated_data"}
	live := []string{stack.ProjectName("MyApp", 1, "feat/x")}
	got := repoWorktrees{Repo: "MyApp", Live: live}.orphanVolumes(all)
	if len(got) != 1 || got[0] != "myapp-wt-2-old_db_data" {
		t.Fatalf("orphans = %v, want only the removed worktree's volume", got)
	}
}

// A worktree whose index was never recorded cannot be turned into the compose
// project name that would claim its volumes, so it looked orphan. doctor prints
// `docker volume rm` lines: guessing wrong loses a live worktree its database.
func TestNoOrphanIsClaimedWhileAWorktreeHasNoIndex(t *testing.T) {
	all := []string{"myapp-wt-1-feat-x_db_data", "myapp-wt-9-gone_db_data"}
	rw := repoWorktrees{Repo: "myapp", Live: nil, Unindexed: []string{"feat/x"}}
	if got := rw.orphanVolumes(all); got != nil {
		t.Fatalf("orphans = %v, want none: one worktree cannot be accounted for", got)
	}
	rw.Unindexed = nil
	rw.Live = []string{stack.ProjectName("myapp", 1, "feat/x")}
	if got := rw.orphanVolumes(all); len(got) != 1 || got[0] != "myapp-wt-9-gone_db_data" {
		t.Fatalf("orphans = %v, want the one nobody owns", got)
	}
}

func TestIntraProjectClashesNameBothBranches(t *testing.T) {
	holders := []portHolder{
		{Port: 26433, Project: "alpha", Branch: "feat/a", Label: "DB_PORT"},
		{Port: 26434, Project: "alpha", Branch: "feat/a", Label: "DB_TEST_PORT"},
		{Port: 26434, Project: "alpha", Branch: "feat/b", Label: "DB_PORT"},
		{Port: 26435, Project: "alpha", Branch: "feat/b", Label: "DB_TEST_PORT"},
		{Port: 26434, Project: "other", Branch: "main", Label: "X"}, // cross-project: not this report's
	}
	got := intraProjectClashes(holders)
	if len(got) != 1 {
		t.Fatalf("expected one line, got %v", got)
	}
	for _, want := range []string{"alpha", "26434", "feat/a DB_TEST_PORT", "feat/b DB_PORT"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("want %q in %q", want, got[0])
		}
	}
}

func TestIntraProjectClashesIgnoreOneBranchTwice(t *testing.T) {
	// The allocator refuses this inside one worktree already; never report it twice.
	holders := []portHolder{
		{Port: 26434, Project: "alpha", Branch: "feat/a", Label: "A"},
		{Port: 26434, Project: "alpha", Branch: "feat/a", Label: "B"},
	}
	if got := intraProjectClashes(holders); len(got) != 0 {
		t.Fatalf("expected nothing, got %v", got)
	}
}
