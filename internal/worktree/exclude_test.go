package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wtm's artifacts sit at the root of the checkout under names no project's
// .gitignore knows, so `git add -A` in a worktree used to commit them.
func TestCreateExcludesItsOwnArtifactsFromGit(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(f.root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("info/exclude should have been written: %v", err)
	}
	for _, want := range []string{".worktrees/", ".git-container", ".db-snapshot", ".wtm-snapshot.yaml", ".wtm-ports.yaml"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("info/exclude should exclude %q:\n%s", want, body)
		}
	}
}

// info/exclude is shared by the whole repository and holds whatever the user
// put there, so the block is rewritten in place rather than appended to.
func TestExcludeKeepsUserEntriesAndDoesNotAccumulate(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.root, ".git", "info", "exclude")
	mustWrite(t, path, "*.local\n")
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(f.root, ".worktrees")); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("second Create: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "*.local") {
		t.Fatalf("the user's own entries must survive:\n%s", body)
	}
	if n := strings.Count(string(body), artifactBlock.Start); n != 1 {
		t.Fatalf("the block should appear once, got %d:\n%s", n, body)
	}
}

// A repository whose info/exclude cannot be written is not a reason to refuse
// the worktree: the exclusion is a convenience, the stack does not need it.
func TestCreateSurvivesAnUnwritableExcludeFile(t *testing.T) {
	f := newFixture(t)
	var out strings.Builder
	o := f.opts("feat/x")
	o.NoStart = true
	o.Out = &out
	// A file where info/ has to go makes both the mkdir and the write fail.
	mustWrite(t, filepath.Join(f.root, ".git", "info"), "")
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(out.String(), "do not commit them") {
		t.Fatalf("the failure should be reported as a warning, got %q", out.String())
	}
}
