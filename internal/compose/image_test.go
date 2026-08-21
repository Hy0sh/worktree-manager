package compose

import "testing"

func TestServiceImageReadsTheMergedConfiguration(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `services:
  db:
    image: postgres:16-alpine
  app:
    build: .
`)
	write(t, dir, "compose.override.yaml", `services:
  db:
    image: mysql:8.4
`)
	if img, ok := ServiceImage(dir, "db"); !ok || img != "mysql:8.4" {
		t.Fatalf("the override must win: %q, %v", img, ok)
	}
	if _, ok := ServiceImage(dir, "app"); ok {
		t.Fatal("a service built from a Dockerfile has no image")
	}
	if _, ok := ServiceImage(dir, "absent"); ok {
		t.Fatal("an unknown service has no image")
	}
}
