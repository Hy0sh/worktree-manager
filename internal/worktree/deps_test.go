package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDepsKeyFollowsTheLockfile(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if depsKey(a) != "" {
		t.Fatal("a directory without a lockfile has no key")
	}
	for _, dir := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if depsKey(a) == "" || depsKey(a) != depsKey(b) {
		t.Fatalf("the same lockfile must give the same key: %q, %q", depsKey(a), depsKey(b))
	}
	if err := os.WriteFile(filepath.Join(b, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\nx: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if depsKey(a) == depsKey(b) {
		t.Fatal("a changed lockfile must change the key")
	}
}
