package stack

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hy0sh/worktree-manager/internal/mark"
)

// portBlock is the section written into a worktree's .env. Its markers are the
// ones worktree-compose used, so a worktree it created keeps working unchanged.
var portBlock = mark.Block{
	Start: "# --- wtc port overrides ---",
	End:   "# --- end wtc ---",
}

// WriteEnvOverrides rewrites the port block of the worktree's .env in place.
// docker compose reads that file from the project directory on its own, which
// is how the rebased ports reach the containers.
func WriteEnvOverrides(worktreeDir string, allocations []Allocation) error {
	path := filepath.Join(worktreeDir, ".env")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	lines := make([]string, 0, len(allocations))
	for _, a := range allocations {
		if a.Var == "" {
			continue // a literal port is rebased through the generated compose file
		}
		lines = append(lines, fmt.Sprintf("%s=%d", a.Var, a.Port))
	}

	return os.WriteFile(path, []byte(portBlock.Rewrite(string(existing), lines)), 0o644)
}
