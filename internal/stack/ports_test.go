package stack

import (
	"os"
	"path/filepath"
	"strings"
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
		got, err := AllocatePort(defaultPort, 1, 7, 0)
		if err != nil {
			t.Fatalf("AllocatePort(%d): %v", defaultPort, err)
		}
		if got != want {
			t.Fatalf("AllocatePort(%d, 1, 7) = %d, want %d", defaultPort, got, want)
		}
	}
}

// Two projects whose database listens on 5432 must not fight over the same
// host port on worktree 1.
func TestProjectOffsetSeparatesProjects(t *testing.T) {
	first, err := AllocatePort(5432, 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AllocatePort(5432, 1, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both projects landed on %d", first)
	}
	if first != 25433 || second != 26433 {
		t.Fatalf("got %d and %d", first, second)
	}
}

func TestAllocatePortFallsBackAboveTheRange(t *testing.T) {
	// 20000 + 60000 + 1 would overflow, so the fallback applies.
	got, err := AllocatePort(60000, 2, 1, 0)
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if got != 60200 {
		t.Fatalf("fallback = %d, want 60200", got)
	}
	if _, err := AllocatePort(65000, 900, 7, 0); err == nil {
		t.Fatal("an unreachable port should be an error")
	}
}

// A literal port is rebased too, but through a generated compose file rather
// than the environment: that is what makes a project usable untouched.
func TestAllocateCoversLiteralPortsAndDetectsCollisions(t *testing.T) {
	services := []compose.ServicePort{
		{Service: "db", Var: "DB_PORT", Host: "5432", Container: "5432"},
		{Service: "legacy", Host: "9999", Container: "9999"},
		{Service: "api", Var: "API_PORT", Host: "8000", Container: "8000"},
	}
	allocs, err := Allocate(services, 1, 7, 0)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(allocs) != 3 {
		t.Fatalf("every published port must be rebased, got %+v", allocs)
	}
	if allocs[1].Port != 30006 || allocs[1].Var != "" {
		t.Fatalf("literal port = %+v", allocs[1])
	}

	if _, err := Allocate([]compose.ServicePort{
		{Service: "a", Var: "A_PORT", Host: "5000", Container: "5000"},
		{Service: "b", Var: "B_PORT", Host: "5000", Container: "5001"},
	}, 1, 7, 0); err == nil {
		t.Fatal("two services sharing a default port must be reported")
	}
}

// Compose appends `ports` when merging, so the list must be replaced whole.
func TestPortsOverrideRestatesEveryPortOfAnOverriddenService(t *testing.T) {
	allocs := []Allocation{
		{Service: "traefik", Port: 20080, Container: "80"},
		{Service: "traefik", Port: 29087, Container: "8080"},
		{Service: "db", Var: "DB_PORT", Port: 25439, Container: "5432"},
	}
	got := PortsOverride(allocs)
	for _, want := range []string{"traefik:", "ports: !override", `"20080:80"`, `"29087:8080"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("override should contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "db:") {
		t.Fatalf("a service reachable through the .env needs no override:\n%s", got)
	}
	if PortsOverride([]Allocation{{Service: "db", Var: "DB_PORT", Port: 25439, Container: "5432"}}) != "" {
		t.Fatal("nothing to override means no file at all")
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
