package stack

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hy0sh/worktree-manager/internal/mark"
	"github.com/Hy0sh/worktree-manager/internal/safefile"
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
	// A symlink here is never this worktree's own file: its content is not
	// carried over, and safefile.Write replaces the link below.
	var existing []byte
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		if existing, err = os.ReadFile(path); err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
	}

	lines := make([]string, 0, len(allocations))
	for _, a := range allocations {
		if a.Var == "" {
			continue // a literal port is rebased through the generated compose file
		}
		lines = append(lines, fmt.Sprintf("%s=%d", a.Var, a.Port))
	}

	// The mode only applies when this creates the file; an existing .env,
	// usually copied from the main repository, keeps its own permissions.
	return safefile.Write(worktreeDir, path, []byte(portBlock.Rewrite(string(existing), lines)), 0o600)
}

// StripEnvOverrides takes the port block back out, leaving every other line
// alone. An adopted worktree survives its stack, and a block naming ports
// nothing listens on any more is worse than no block at all.
func StripEnvOverrides(worktreeDir string) error {
	path := filepath.Join(worktreeDir, ".env")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	next := portBlock.Strip(string(existing))
	if next == string(existing) {
		return nil
	}
	return safefile.Write(worktreeDir, path, []byte(next), info.Mode().Perm())
}
