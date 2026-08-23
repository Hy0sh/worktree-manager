// Package safefile is tested against the three ways a checkout can lay a
// symlink across a write path: at the leaf, at an intermediate directory,
// and legitimately at the root itself (macOS temp dirs).
package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesFileAndParents(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sub", "app.env")
	if err := Write(root, path, []byte("A=1\n"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "A=1\n" {
		t.Fatalf("read back %q, %v", data, err)
	}
}

func TestWriteReplacesALeafSymlink(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".env")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, link, []byte("PORT=1\n"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "precious" {
		t.Fatalf("victim was written through: %q", data)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("dst should now be a regular file, got %v, %v", info, err)
	}
}

func TestWriteRefusesAnIntermediateSymlinkEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "backend")); err != nil {
		t.Fatal(err)
	}
	err := Write(root, filepath.Join(root, "backend", "app.env"), []byte("A=1\n"), 0o644)
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "app.env")); !os.IsNotExist(statErr) {
		t.Fatal("the file landed outside the root")
	}
}

func TestWriteToleratesASymlinkedRoot(t *testing.T) {
	// macOS hands out symlinked temp directories; a root reached through a
	// link is still the same tree once both sides are resolved.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, filepath.Join(link, "x.env"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatalf("Write through a symlinked root: %v", err)
	}
}
