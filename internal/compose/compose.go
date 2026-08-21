// Package compose locates compose files and inspects how they declare ports.
package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// Base is the file wtc reads. It stops at the first match and never looks at
// override files, so a port parametrised only in an override stays invisible
// to it.
func Base(dir string) (string, error) {
	files, err := Files(dir)
	if err != nil {
		return "", err
	}
	return files[0], nil
}

var (
	rawPort   = regexp.MustCompile(`^\s*-\s*"?(\d+):(\d+)`)
	paramPort = regexp.MustCompile(`^\s*-\s*"?\$\{`)
)

// Ports counts how the base compose file declares its host ports. wtc can only
// isolate a worktree when they read ${VAR:-default}; anything hardcoded would
// collide with the main stack.
func Ports(path string) (raw []string, parametrised int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case paramPort.MatchString(line):
			parametrised++
		case rawPort.MatchString(line):
			raw = append(raw, strings.TrimSpace(line))
		}
	}
	return raw, parametrised, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
