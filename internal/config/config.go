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

// Environment overrides, mostly so tests never touch the real home directory.
const (
	EnvConfigDir  = "WTM_CONFIG_DIR"
	EnvBackupsDir = "WTM_BACKUPS_DIR"
)

// Project is one registered repository.
type Project struct {
	Dir        string `json:"dir"`
	BaseBranch string `json:"base_branch,omitempty"`
	Dump       bool   `json:"dump,omitempty"`
	// GitContainer creates the .git-container symlinks. Opt-in: it only helps
	// projects whose compose bind-mounts the git directory into a container
	// (macOS/VirtioFS cannot mount the pointer file a linked worktree uses).
	GitContainer bool `json:"git_container,omitempty"`
	// PortOffset shifts every rebased port of this project. The allocation
	// formula only knows the default port, the worktree index and the stride,
	// so without it two projects whose database listens on 5432 would fight
	// over the same host port. Zero for the first project, which keeps the
	// ports it already had.
	PortOffset int `json:"port_offset,omitempty"`
	// WorktreeIndices pins each branch to the index its stack was created
	// with. The index feeds the port formula and the compose project name,
	// so deriving it from git's listing order (which resorts alphabetically)
	// renumbers running stacks; recording it here is what keeps it stable.
	WorktreeIndices map[string]int `json:"worktree_indices,omitempty"`
	Backup          *Backup        `json:"backup,omitempty"`
}

// Defaults for the most common docker compose layout.
const (
	DefaultDBService = "db"
	DefaultDBUser    = "postgres"
	// DefaultMigrationsPath covers Django's app/migrations, Prisma's
	// prisma/migrations and MikroORM's src/database/migrations.
	DefaultMigrationsPath = "*migrations/*"
	// DatabasePlaceholder is replaced by the throwaway database name in
	// migrate_command and in every env value.
	DatabasePlaceholder = "{{database}}"
)

// Backup describes how to rebuild a schema. Everything here differs from one
// project to the next: a survey of four real projects found four different
// migration commands, three app service names, two database service names,
// two database users and two ways of telling the app which database to use.
type Backup struct {
	DBService  string `json:"db_service,omitempty"`  // default "db"
	DBUser     string `json:"db_user,omitempty"`     // default "postgres"
	AppService string `json:"app_service,omitempty"` // required
	// DepsCommand runs before the migration in the disposable container, for
	// projects that install their dependencies at container startup rather
	// than baking them into the image (a fresh container has none).
	DepsCommand string `json:"deps_command,omitempty"`
	// MigrateCommand builds the schema, e.g. "python manage.py migrate",
	// "npx prisma migrate deploy", "mikro-orm migration:up". Required.
	MigrateCommand string `json:"migrate_command,omitempty"`
	// MigrationsPath is the git pathspec of the migration files, used to tell
	// whether a dump has fallen behind. The default matches the layout of
	// Django, Prisma and MikroORM alike.
	MigrationsPath string `json:"migrations_path,omitempty"`
	// Env tells the app which database to target: {"DB_NAME": "{{database}}"}
	// or {"DATABASE_URL": "postgresql://user:pass@db:5432/{{database}}"}.
	Env map[string]string `json:"env,omitempty"`
}

// BackupConfig returns the project's backup settings with defaults applied.
func (p Project) BackupConfig() Backup {
	b := Backup{}
	if p.Backup != nil {
		b = *p.Backup
	}
	if b.DBService == "" {
		b.DBService = DefaultDBService
	}
	if b.DBUser == "" {
		b.DBUser = DefaultDBUser
	}
	if b.MigrationsPath == "" {
		b.MigrationsPath = DefaultMigrationsPath
	}
	return b
}

// Validate reports what is missing before a refresh can run.
func (b Backup) Validate() error {
	var missing []string
	if b.AppService == "" {
		missing = append(missing, "app_service")
	}
	if b.MigrateCommand == "" {
		missing = append(missing, "migrate_command")
	}
	if len(missing) > 0 {
		return fmt.Errorf("incomplete backup configuration, missing %s (fill in the project's `backup` entry in config.json)", strings.Join(missing, " and "))
	}
	return nil
}

// Expand substitutes the throwaway database name.
func Expand(s, database string) string {
	return strings.ReplaceAll(s, DatabasePlaceholder, database)
}

// Config is the whole registry.
type Config struct {
	DefaultBaseBranch string             `json:"default_base_branch,omitempty"`
	Projects          map[string]Project `json:"projects"`
}

// Dir is where config.json and the central backups live.
func Dir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory not found: %w", err)
	}
	return filepath.Join(home, ".config", "wtm"), nil
}

// Path is the config file location.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// BackupsDir is the single place where dumps are stored, shared by every
// worktree through a symlink.
func BackupsDir() (string, error) {
	if d := os.Getenv(EnvBackupsDir); d != "" {
		return d, nil
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "backups"), nil
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

// Save writes the registry, creating the directory when needed.
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
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// NextPortOffset returns a free offset for a new project, in steps of 1000.
// The first project keeps 0 so its existing worktrees are untouched.
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

// Names lists the registered projects, sorted.
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

// Get returns a project, listing the known ones when it is unknown.
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

// ResolveCurrent finds the project whose dir is the given repository root.
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
