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
	// WtcBin overrides where worktree-compose lives. Left empty, the project's
	// own devDependency is used when present, otherwise a global install.
	WtcBin string  `json:"wtc_bin,omitempty"`
	Backup *Backup `json:"backup,omitempty"`
}

// Defaults for the most common docker compose layout.
const (
	DefaultDBService = "db"
	DefaultDBUser    = "postgres"
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
		return fmt.Errorf("configuration de backup incomplète, il manque %s — complète l'entrée `backup` du projet dans config.json", strings.Join(missing, " et "))
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
		return "", fmt.Errorf("répertoire personnel introuvable: %w", err)
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
		return nil, fmt.Errorf("lecture de %s: %w", path, err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s est un JSON invalide: %w", path, err)
	}
	if cfg.Projects == nil {
		cfg.Projects = map[string]Project{}
	}
	return cfg, nil
}

// Save writes the registry, creating the directory when needed.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("création de %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
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
		known := "aucun projet enregistré"
		if len(c.Projects) > 0 {
			known = "projets enregistrés: " + strings.Join(c.Names(), ", ")
		}
		return Project{}, fmt.Errorf("projet inconnu %q (%s) — voir `wtm project list`", name, known)
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
	return "", Project{}, fmt.Errorf("le dépôt %s n'est pas enregistré — lance `wtm project create <nom> --dir %s`", repoRoot, repoRoot)
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
