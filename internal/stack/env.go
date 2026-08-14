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

// ReadPorts returns the port assignments recorded in the worktree .env.
func ReadPorts(worktreeDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(worktreeDir, ".env"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var (
		ports  []string
		inside bool
	)
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == blockStart:
			inside = true
		case trimmed == blockEnd:
			return ports, nil
		case inside && trimmed != "":
			ports = append(ports, trimmed)
		}
	}
	return ports, nil
}

// ReadPortValues returns the allocated ports keyed by environment variable.
func ReadPortValues(worktreeDir string) (map[string]string, error) {
	lines, err := ReadPorts(worktreeDir)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(lines))
	for _, line := range lines {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values, nil
}
