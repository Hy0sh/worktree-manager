package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFilesDetection(t *testing.T) {
	dir := t.TempDir()
	if _, err := Files(dir); err == nil {
		t.Fatal("a project without a compose file should be an error")
	}
	base := write(t, dir, "docker-compose.yml", "services: {}\n")
	files, err := Files(dir)
	if err != nil || len(files) != 1 || files[0] != base {
		t.Fatalf("files = %v, err = %v", files, err)
	}
	override := write(t, dir, "docker-compose.override.yml", "services: {}\n")
	if files, err = Files(dir); err != nil || len(files) != 2 || files[1] != override {
		t.Fatalf("the override should be picked up too: %v %v", files, err)
	}
}

// An override alone is no stack: docker compose refuses to run on one.
func TestHasNeedsABaseFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.override.yaml", "services: {}\n")
	if Has(dir) {
		t.Fatal("an override alone must not count as a stack")
	}
	write(t, dir, "compose.yaml", "services: {}\n")
	if !Has(dir) {
		t.Fatal("a base file is a stack")
	}
}
