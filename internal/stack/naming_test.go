package stack

import "testing"

func TestProjectPrefix(t *testing.T) {
	if got := ProjectPrefix("My App", 3); got != "my-app-wt-3-" {
		t.Fatalf("got %q", got)
	}
}

// The compose project name is the only handle on a worktree stack's containers
// and volumes, and it has to match what worktree-compose produced, character
// for character, or an existing stack becomes unreachable.
func TestProjectNameMatchesTheFormerTool(t *testing.T) {
	if got := ProjectName("my-app", 1, "feat/wtm-e2e"); got != "my-app-wt-1-feat-wtm-e2e" {
		t.Fatalf("ProjectName = %q", got)
	}
	if got := ProjectName("my-app", 3, "Fix/GAL_42--Weird"); got != "my-app-wt-3-fix-gal-42-weird" {
		t.Fatalf("ProjectName = %q", got)
	}
}
