// Package config reads and writes the project registry used by wtm.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FallbackBaseBranch applies when neither the project nor the config names one.
const FallbackBaseBranch = "develop"

type Config struct {
	DefaultBaseBranch string             `json:"default_base_branch,omitempty"`
	Projects          map[string]Project `json:"projects"`
}

// Load reads the registry. A missing file is an empty registry, not an error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{Projects: map[string]Project{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s is invalid JSON: %w", path, err)
	}
	if cfg.Projects == nil {
		cfg.Projects = map[string]Project{}
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	// The registry can hold a DATABASE_URL with its password, so it is kept
	// readable by its owner alone.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Readers (Load) run outside the lock, so writing in place would let one
	// see a truncated file: the temp file and the rename make it atomic.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NextPortOffset steps by 1000, and the first project keeps 0 so its existing
// worktrees are untouched.
func (c *Config) NextPortOffset() int {
	taken := map[int]bool{}
	for _, p := range c.Projects {
		taken[p.PortOffset] = true
	}
	for offset := 0; ; offset += 1000 {
		if !taken[offset] {
			return offset
		}
	}
}

func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Projects))
	for name := range c.Projects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Has reports whether a name is registered, which is how the CLI tells a
// project argument from a branch argument.
func (c *Config) Has(name string) bool {
	_, ok := c.Projects[name]
	return ok
}

func (c *Config) Get(name string) (Project, error) {
	p, ok := c.Projects[name]
	if !ok {
		known := "no registered project"
		if len(c.Projects) > 0 {
			known = "registered projects: " + strings.Join(c.Names(), ", ")
		}
		return Project{}, fmt.Errorf("unknown project %q (%s), see `wtm project list`", name, known)
	}
	return p, nil
}

func (c *Config) ResolveCurrent(repoRoot string) (string, Project, error) {
	for _, name := range c.Names() {
		if samePath(c.Projects[name].Dir, repoRoot) {
			return name, c.Projects[name], nil
		}
	}
	return "", Project{}, fmt.Errorf("repository %s is not registered, run `wtm project create <name> --dir %s`", repoRoot, repoRoot)
}

// BaseBranchFor applies the project > config > fallback precedence.
func (c *Config) BaseBranchFor(p Project) string {
	if p.BaseBranch != "" {
		return p.BaseBranch
	}
	if c.DefaultBaseBranch != "" {
		return c.DefaultBaseBranch
	}
	return FallbackBaseBranch
}

// samePath compares directories, tolerating trailing slashes and the symlinked
// temp directories macOS hands out.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && filepath.Clean(ra) == filepath.Clean(rb)
}
