package main

import (
	"strings"
	"testing"
)

// Measured on two real projects: shop's db_test (offset 1000, default 5433)
// and station's kratos (offset 2000, default 4433) both land on 26434, because
// the offset step is smaller than the spread of the default ports it shifts.
func TestPortClashesReportsTwoProjectsOnTheSamePort(t *testing.T) {
	lines := portClashes([]portHolder{
		{Port: 26434, Project: "shop", Branch: "feat/x", Label: "db_test:5432"},
		{Port: 26434, Project: "station", Branch: "feat/y", Label: "kratos:4433"},
		{Port: 26433, Project: "shop", Branch: "feat/x", Label: "db:5432"},
	})
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want the single clash", lines)
	}
	for _, want := range []string{"26434", "shop/feat/x", "station/feat/y"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line should mention %q, got %q", want, lines[0])
		}
	}
}

// Two branches of one project never clash: the allocator gives each index its
// own port, and refuses within a project anyway. Reporting them would be noise.
func TestPortClashesIgnoresOneProjectWithItself(t *testing.T) {
	lines := portClashes([]portHolder{
		{Port: 26434, Project: "shop", Branch: "feat/x", Label: "db:5432"},
		{Port: 26434, Project: "shop", Branch: "feat/y", Label: "db:5432"},
	})
	if len(lines) != 0 {
		t.Fatalf("lines = %v, want none", lines)
	}
}

func TestOrphanVolumesKeepsWhatALiveWorktreeOwns(t *testing.T) {
	all := []string{
		"gallia-utopia-wt-1-worktree-parsed_postgres_data",
		"gallia-utopia-wt-2-fix-migration_postgres_data",
		"gallia-utopia-wt-2-fix-migration_rustfs_data",
		"other-project-wt-1-feat-x_data", // another repository's, not ours to report
		"gallia-utopia_postgres_data",    // the main stack, not a worktree
	}
	orphans := orphanVolumes(all, "gallia-utopia", []string{"gallia-utopia-wt-1-worktree-parsed"})
	want := []string{
		"gallia-utopia-wt-2-fix-migration_postgres_data",
		"gallia-utopia-wt-2-fix-migration_rustfs_data",
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
	orphans := orphanVolumes([]string{"shop-wt-1-feat-x_pgdata", "shop_pgdata"}, "shop", nil)
	if len(orphans) != 1 || orphans[0] != "shop-wt-1-feat-x_pgdata" {
		t.Fatalf("orphans = %v", orphans)
	}
}
