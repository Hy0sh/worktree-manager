package compose

import (
	"slices"
	"testing"
)

func TestBuiltServicesAreTheOnesWithABuild(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", "services:\n  api:\n    build: .\n  db:\n    image: postgres\n  worker:\n    build:\n      context: .\n")
	if got := BuiltServices(dir); !slices.Equal(got, []string{"api", "worker"}) {
		t.Fatalf("built = %v", got)
	}
}
