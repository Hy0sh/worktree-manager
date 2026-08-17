package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Markers of the block written into a worktree's .env. Identical to the ones
// worktree-compose used, so a worktree it created keeps working unchanged.
const (
	blockStart = "# --- wtc port overrides ---"
	blockEnd   = "# --- end wtc ---"
)

// WriteEnvOverrides rewrites the port block of the worktree's .env in place.
// docker compose reads that file from the project directory on its own, which
// is how the rebased ports reach the containers.
func WriteEnvOverrides(worktreeDir string, allocations []Allocation) error {
	path := filepath.Join(worktreeDir, ".env")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var b strings.Builder
	if body := strings.TrimRight(StripBlock(string(existing)), "\n"); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString(blockStart + "\n")
	for _, a := range allocations {
		if a.Var == "" {
			continue // a literal port is rebased through the generated compose file
		}
		fmt.Fprintf(&b, "%s=%d\n", a.Var, a.Port)
	}
	b.WriteString(blockEnd + "\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// StripBlock removes a previously written block, so rewriting is idempotent
// rather than cumulative.
func StripBlock(content string) string {
	start := strings.Index(content, blockStart)
	if start == -1 {
		return content
	}
	end := strings.Index(content[start:], blockEnd)
	if end == -1 {
		return content
	}
	return content[:start] + content[start+end+len(blockEnd):]
}
