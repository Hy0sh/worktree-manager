// Package compose locates compose files and inspects how they declare ports.
package compose

import (
	"fmt"
	"os"
	"path/filepath"
)

// baseNames is docker compose's own lookup order.
var baseNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// OverrideNames is exported so provisioning copies exactly the files this
// package detects; two lists drifting apart is how a detected override once
// ended up referenced by -f without existing in the worktree.
var OverrideNames = []string{
	"compose.override.yaml", "compose.override.yml",
	"docker-compose.override.yaml", "docker-compose.override.yml",
}

// Files reproduces docker compose's auto-detection, needed whenever an extra
// -f is passed since that forces every file to be named explicitly.
func Files(dir string) ([]string, error) {
	var files []string
	for _, name := range baseNames {
		if path := filepath.Join(dir, name); exists(path) {
			files = append(files, path)
			break
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no compose file found in %s", dir)
	}
	for _, name := range OverrideNames {
		if path := filepath.Join(dir, name); exists(path) {
			files = append(files, path)
			break
		}
	}
	return files, nil
}

// Has says whether dir holds a compose file, which is whether it has a stack.
func Has(dir string) bool {
	_, err := Files(dir)
	return err == nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
