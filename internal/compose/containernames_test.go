package compose

import (
	"testing"
)

// A pinned container_name is the one thing wtm cannot rebase, so it has to be
// spotted from the project's files rather than discovered when docker refuses
// the second container.
func TestPinnedContainerNamesFindsThemAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `services:
  db:
    image: postgres:16
    container_name: acme-db
  api:
    build: .
`)
	write(t, dir, "compose.override.yaml", `services:
  api:
    container_name: acme-api
  worker:
    image: acme/worker
`)
	pinned, err := PinnedContainerNames(dir)
	if err != nil {
		t.Fatalf("PinnedContainerNames: %v", err)
	}
	if len(pinned) != 2 || pinned[0] != "db" || pinned[1] != "api" {
		t.Fatalf("pinned = %v, want [db api] in file order", pinned)
	}
}

func TestPinnedContainerNamesIsEmptyOnAnIsolatableProject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `services:
  db:
    image: postgres:16
    ports:
      - "5432:5432"
`)
	pinned, err := PinnedContainerNames(dir)
	if err != nil {
		t.Fatalf("PinnedContainerNames: %v", err)
	}
	if len(pinned) != 0 {
		t.Fatalf("pinned = %v, want none", pinned)
	}
}
