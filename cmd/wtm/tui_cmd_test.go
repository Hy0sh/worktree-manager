package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestTuiNeedsATerminal(t *testing.T) {
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: t.TempDir()}}}
	a, _, _ := newTestApp(t, cfg, "", func(execx.Cmd) (execx.Result, error) { return execx.Result{}, nil })
	cmd := newTUICmd(a)
	err := cmd.RunE(cmd, []string{"myapp"})
	if err == nil || !strings.Contains(err.Error(), "wtm list") {
		t.Fatalf("RunE = %v, want a refusal pointing at `wtm list`", err)
	}
}

// Open all day, the dashboard outlives the registry it started with: an index
// recorded since, by a start from another terminal, has to show.
func TestTheDashboardReadsTheRegistryAgainOnEveryListing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myapp")
	cfg := &config.Config{Projects: map[string]config.Project{"myapp": {Dir: dir}}}
	a, _, _ := newTestApp(t, cfg, "", func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree list") {
			return execx.Result{Stdout: porcelainOf(dir,
				"worktree "+filepath.Join(dir, ".worktrees", "feat", "x")+"\nHEAD abc\nbranch refs/heads/feat/x")}, nil
		}
		return execx.Result{}, nil
	})
	list := a.dashboardSource("myapp").List

	entries, err := list(context.Background())
	if err != nil || len(entries) != 1 || entries[0].ComposeProject != "" {
		t.Fatalf("before any start: entries = %+v, err = %v", entries, err)
	}
	if err := config.WithLock(a.cfgPath, func(c *config.Config) error {
		p := c.Projects["myapp"]
		p.WorktreeIndices = map[string]int{"feat/x": 2}
		c.Projects["myapp"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if entries, err = list(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := entries[0].ComposeProject; got != "myapp-wt-2-feat-x" {
		t.Fatalf("ComposeProject = %q, want the index recorded since", got)
	}
}
