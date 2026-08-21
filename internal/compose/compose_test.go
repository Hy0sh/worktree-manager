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

// Only the first base file matters for wtc, which never reads overrides.
func TestBaseIgnoresOverrides(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "compose.yaml", "services: {}\n")
	write(t, dir, "compose.override.yaml", "services: {}\n")
	got, err := Base(dir)
	if err != nil {
		t.Fatalf("Base: %v", err)
	}
	if got != base {
		t.Fatalf("Base = %q, want %q", got, base)
	}
}

func TestPortsSeparatesHardcodedFromParametrised(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "compose.yaml", `services:
  db:
    ports:
      - "5432:5432"
  api:
    ports:
      - 8080:8080
  web:
    ports:
      - "${FRONTEND_PORT:-3000}:3000"
  extra:
    ports:
      - "${API_PORT:-8000}:8000"
`)
	raw, parametrised, err := Ports(path)
	if err != nil {
		t.Fatalf("Ports: %v", err)
	}
	if parametrised != 2 {
		t.Fatalf("parametrised = %d, want 2", parametrised)
	}
	if len(raw) != 2 {
		t.Fatalf("raw = %v, want 2 entries (quoted and unquoted forms)", raw)
	}
	if raw[0] != "5432:5432" || raw[1] != "8080:8080" {
		t.Fatalf("raw = %v", raw)
	}
}
