package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Top-level blocks indent their entries exactly like services do, so a scan
// that ignores the block it is in would offer a volume as an application
// service.
func TestServicesIgnoresTheOtherTopLevelBlocks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), `services:
  db:
    image: postgres
  backend:
    build: .
volumes:
  pgdata:
  node_modules:
networks:
  default:
`)
	got, err := Services(dir)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if strings.Join(got, ",") != "db,backend" {
		t.Fatalf("services = %v, want [db backend] in declaration order", got)
	}
}

// An override that adds a service must show up too, and one that only tweaks
// an existing service must not duplicate it.
func TestServicesMergesTheOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), `services:
  db:
    image: postgres
`)
	writeFile(t, filepath.Join(dir, "compose.override.yaml"), `services:
  db:
    ports:
      - "5432:5432"
  mailhog:
    image: mailhog/mailhog
`)
	got, err := Services(dir)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if strings.Join(got, ",") != "db,mailhog" {
		t.Fatalf("services = %v, want [db mailhog]", got)
	}
}

func TestServicesOnAProjectWithoutCompose(t *testing.T) {
	if _, err := Services(t.TempDir()); err == nil {
		t.Fatal("a directory without a compose file should report it")
	}
}
