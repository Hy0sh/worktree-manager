package worktree

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/stack"
)

func TestStackEnvMarksDepsExternal(t *testing.T) {
	locked := t.TempDir()
	if err := os.WriteFile(filepath.Join(locked, "go.sum"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"with a lockfile": locked, "without": t.TempDir()} {
		if !slices.Contains(stackEnv(Options{}, stack.Worktree{}, dir), "WTM_DEPS_EXTERNAL=true") {
			t.Fatalf("%s: WTM_DEPS_EXTERNAL=true missing", name)
		}
	}
}

func TestDepsKeyFollowsTheLockfile(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if DepsKey(a) != "" {
		t.Fatal("a directory without a lockfile has no key")
	}
	for _, dir := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if DepsKey(a) == "" || DepsKey(a) != DepsKey(b) {
		t.Fatalf("the same lockfile must give the same key: %q, %q", DepsKey(a), DepsKey(b))
	}
	if err := os.WriteFile(filepath.Join(b, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\nx: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if DepsKey(a) == DepsKey(b) {
		t.Fatal("a changed lockfile must change the key")
	}
}
