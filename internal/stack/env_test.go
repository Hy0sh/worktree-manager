package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The block keeps the markers the former tool used, so an existing worktree
// keeps working and rewriting stays idempotent rather than cumulative.
func TestWriteEnvOverridesIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("SECRET=keep-me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allocs := []Allocation{
		{Service: "db", Var: "DB_PORT", Port: 25439},
		{Service: "api", Var: "API_PORT", Port: 28007},
	}
	for range 3 {
		if err := WriteEnvOverrides(dir, allocs); err != nil {
			t.Fatalf("WriteEnvOverrides: %v", err)
		}
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !strings.Contains(content, "SECRET=keep-me") {
		t.Fatalf("existing content must survive:\n%s", content)
	}
	if n := strings.Count(content, "# --- wtc port overrides ---"); n != 1 {
		t.Fatalf("expected exactly one block after three writes, got %d:\n%s", n, content)
	}
	if !strings.Contains(content, "DB_PORT=25439") || !strings.Contains(content, "API_PORT=28007") {
		t.Fatalf("ports missing:\n%s", content)
	}
}

func TestWriteEnvOverridesCreatesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEnvOverrides(dir, []Allocation{{Var: "DB_PORT", Port: 25439}}); err != nil {
		t.Fatalf("WriteEnvOverrides: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("the .env should have been created: %v", err)
	}
	if !strings.Contains(string(body), "DB_PORT=25439") {
		t.Fatalf("port missing:\n%s", body)
	}
}
