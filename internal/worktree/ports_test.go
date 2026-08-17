package worktree

import (
	"context"
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
