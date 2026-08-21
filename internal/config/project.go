package config

import (
	"fmt"
	"strings"
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
	DefaultDBEngine  = "postgres"
	// DefaultDBPath is Django's convention, the most common sqlite layout.
	DefaultDBPath = "db.sqlite3"
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
	DBService string `json:"db_service,omitempty"` // default "db"
	DBUser    string `json:"db_user,omitempty"`    // default "postgres"
	// DBEngine names the database engine (see internal/dbengine). Empty means
	// postgres, which is what every project registered before this field
	// existed means.
	DBEngine string `json:"db_engine,omitempty"`
	// DBPath is where a file-based engine's database lives, relative to the
	// project root. The dump is copied there in each worktree. Ignored by
	// server engines.
	DBPath     string `json:"db_path,omitempty"`
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
	if b.DBEngine == "" {
		b.DBEngine = DefaultDBEngine
	}
	if b.DBPath == "" {
		b.DBPath = DefaultDBPath
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
