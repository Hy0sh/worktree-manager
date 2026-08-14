package stack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/compose"
)

// These are the ports worktree-compose actually assigned to worktree 1 of a
// real project with stride 7. They must not move.
func TestAllocatePortMatchesTheFormerTool(t *testing.T) {
	cases := map[int]int{
		5432: 25439, // db
		8000: 28007, // backend
		3000: 23007, // frontend
		9000: 29007, // object storage
		1025: 21032, // smtp
	}
	for defaultPort, want := range cases {
		got, err := AllocatePort(defaultPort, 1, 7)
		if err != nil {
			t.Fatalf("AllocatePort(%d): %v", defaultPort, err)
		}
		if got != want {
			t.Fatalf("AllocatePort(%d, 1, 7) = %d, want %d", defaultPort, got, want)
		}
	}
}

func TestAllocatePortFallsBackAboveTheRange(t *testing.T) {
	// 20000 + 60000 + 1 would overflow, so the fallback applies.
	got, err := AllocatePort(60000, 2, 1)
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if got != 60200 {
		t.Fatalf("fallback = %d, want 60200", got)
	}
	if _, err := AllocatePort(65000, 900, 7); err == nil {
		t.Fatal("an unreachable port should be an error")
	}
}

func TestAllocateSkipsHardcodedPortsAndDetectsCollisions(t *testing.T) {
	services := []compose.ServicePort{
		{Service: "db", Var: "DB_PORT", Host: "5432", Container: "5432"},
		{Service: "legacy", Host: "9999", Container: "9999"}, // no variable
		{Service: "api", Var: "API_PORT", Host: "8000", Container: "8000"},
	}
	allocs, err := Allocate(services, 1, 7)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(allocs) != 2 {
		t.Fatalf("a port without a variable cannot be rebased, got %+v", allocs)
	}
	if allocs[0].Port != 25439 || allocs[1].Port != 28007 {
		t.Fatalf("allocations = %+v", allocs)
	}

	// Two services whose defaults differ by exactly the stride land on the
	// same port at index 1.
	clashing := []compose.ServicePort{
		{Service: "a", Var: "A_PORT", Host: "5000", Container: "5000"},
		{Service: "b", Var: "B_PORT", Host: "5007", Container: "5007"},
	}
	if _, err := Allocate(clashing, 1, 0); err != nil {
		t.Fatalf("stride 0 keeps them apart: %v", err)
	}
	if _, err := Allocate([]compose.ServicePort{
		{Service: "a", Var: "A_PORT", Host: "5000", Container: "5000"},
		{Service: "b", Var: "B_PORT", Host: "5000", Container: "5001"},
	}, 1, 7); err == nil {
		t.Fatal("two services sharing a default port must be reported")
	}
}

func TestStrideReadsTheProjectConfiguration(t *testing.T) {
	dir := t.TempDir()
	if got := Stride(dir); got != DefaultStride {
		t.Fatalf("Stride without configuration = %d, want %d", got, DefaultStride)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"wtc":{"portStride":3}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Stride(dir); got != 3 {
		t.Fatalf("Stride from package.json = %d, want 3", got)
	}
	// .wtcrc.json wins, as it did before.
	if err := os.WriteFile(filepath.Join(dir, ".wtcrc.json"), []byte(`{"portStride":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Stride(dir); got != 7 {
		t.Fatalf("Stride = %d, want 7", got)
	}
}
