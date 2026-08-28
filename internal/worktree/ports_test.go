package worktree

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A raw .env block ("BACKEND_PORT=28007 DB_PORT=25439") tells nobody where to
// point a browser. Pair each service with the port it actually listens on.
func TestCreateListsServiceEndpointsAfterStart(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  backend:
    ports:
      - "${BACKEND_PORT:-8000}:8000"
  db:
    ports:
      - "${DB_PORT:-5432}:5432"
  legacy:
    ports:
      - "9000:9000"
`)
	// Copied into the worktree by Create, then read back as if wtc wrote it.
	mustWrite(t, filepath.Join(f.root, ".env"), "FOO=bar\n\n# --- wtc port overrides ---\nBACKEND_PORT=28007\nDB_PORT=25439\n# --- end wtc ---\n")

	var out strings.Builder
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "backend  http://localhost:28007") {
		t.Fatalf("a web service should get a clickable URL, got:\n%s", got)
	}
	if !strings.Contains(got, "db       localhost:25439") {
		t.Fatalf("a database should be listed without an http scheme, got:\n%s", got)
	}
	// A hardcoded port is rebased through the generated compose file, so it
	// belongs in the list like any other.
	if !strings.Contains(got, "legacy   localhost:29007") {
		t.Fatalf("a rebased hardcoded port should be listed too:\n%s", got)
	}
}

// The port block in the worktree's own .env is the whole isolation mechanism,
// and no test reached it: the fixture answered every `ls-files` successfully,
// so git looked like it versioned .env and wtm took the branch that writes
// nothing. Without this, the block could stop being written and the suite would
// stay green while every new worktree kept the main stack's ports.
func TestCreateWritesTheAllocatedPortsIntoTheWorktreeEnv(t *testing.T) {
	f := newFixture(t)
	if err := Create(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", ".env"))
	if err != nil {
		t.Fatalf("no .env in the worktree: %v", err)
	}
	body := string(got)
	for _, want := range []string{"# --- wtc port overrides ---", "DB_PORT=", "BACKEND_PORT=", "# --- end wtc ---"} {
		if !strings.Contains(body, want) {
			t.Fatalf(".env = %q, want %q in it", body, want)
		}
	}
	// The point of the block is that these are not the compose defaults.
	for _, unwanted := range []string{"DB_PORT=5432", "BACKEND_PORT=8000"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf(".env keeps the main stack's port: %q", body)
		}
	}
}

// A tracked .env belongs to the project, and writing into it would dirty the
// worktree on every start. The generated compose file carries the ports then.
func TestCreateLeavesATrackedEnvAloneAndSaysSo(t *testing.T) {
	f := newFixture(t)
	f.envTracked = true
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=1")
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "wtc port overrides") {
		t.Fatalf("a tracked .env must not be written into, got %q", got)
	}
	if !strings.Contains(out.String(), ".env is tracked by git") {
		t.Fatalf("the note should say where the ports went instead:\n%s", out.String())
	}
	// The note above promises the ports are in that file, so every one of them
	// has to be: leaving out the parametrised ones, which is what the generated
	// file used to do, isolated nothing at all here.
	gen, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", portsOverride))
	if err != nil {
		t.Fatalf("the generated compose file should carry them: %v", err)
	}
	for _, want := range []string{"db:", "backend:", "ports: !override", `:5432"`, `:8000"`} {
		if !strings.Contains(string(gen), want) {
			t.Fatalf("%s = %q, want %q in it", portsOverride, gen, want)
		}
	}
	for _, unwanted := range []string{`"5432:5432"`, `"8000:8000"`} {
		if strings.Contains(string(gen), unwanted) {
			t.Fatalf("%s keeps the main stack's port: %q", portsOverride, gen)
		}
	}
}
