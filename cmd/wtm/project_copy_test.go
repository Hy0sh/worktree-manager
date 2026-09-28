package main

import (
	"slices"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

func TestCopyReplacesTheListAndAnEmptyOneClearsIt(t *testing.T) {
	f, cmd := parsedFlags(t, "--copy", ".env.local", "--copy", "config/*.local.json")
	u, err := f.update(cmd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	p, _ := u.Apply(config.Project{Copy: []string{"old"}})
	if want := []string{".env.local", "config/*.local.json"}; !slices.Equal(p.Copy, want) {
		t.Fatalf("copy = %v, want %v", p.Copy, want)
	}

	f, cmd = parsedFlags(t, "--copy", "")
	u, err = f.update(cmd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p, _ = u.Apply(p); len(p.Copy) != 0 {
		t.Fatalf("copy = %v, --copy '' should clear it", p.Copy)
	}
}

// The pattern is joined under the project root and under every worktree.
func TestCopyRefusesAPatternLeavingTheProject(t *testing.T) {
	for _, pattern := range []string{"../secrets.json", "/etc/passwd", "config/[.json"} {
		f, cmd := parsedFlags(t, "--copy", pattern)
		if _, err := f.update(cmd); err == nil {
			t.Fatalf("--copy %q should be refused", pattern)
		}
	}
}
