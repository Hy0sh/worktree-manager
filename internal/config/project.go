package config

import (
	"fmt"
	"strings"
)

type Project struct {
	Dir        string `json:"dir"`
	BaseBranch string `json:"base_branch,omitempty"`
	Dump       bool   `json:"dump,omitempty"`
	// GitContainer creates the .git-container symlinks. Opt-in: it only helps
	// projects whose compose bind-mounts the git directory into a container
	// (macOS/VirtioFS cannot mount the pointer file a linked worktree uses).
	GitContainer bool `json:"git_container,omitempty"`
	// PortOffset shifts every rebased port: the formula knows only the default
	// port, the index and the stride, so without it two projects whose database
	// listens on 5432 clash. Zero for the first, which keeps its ports.
	PortOffset int `json:"port_offset,omitempty"`
	// WorktreeIndices pins each branch to the index its stack was created with.
	// Deriving it from git's listing order, which resorts alphabetically, would
	// renumber running stacks: the index feeds ports and the compose project name.
	WorktreeIndices map[string]int `json:"worktree_indices,omitempty"`
	// PostCreate runs in the application container once a new worktree's stack
	// answers. The dump holds no seed data, so this is where it comes from, e.g.
	// "python manage.py seed_data && python manage.py create_dev_users".
	PostCreate string `json:"post_create,omitempty"`
	// ReadyTimeout and ReadyInterval bound the wait a new worktree grants each
	// service before post_create runs, as durations ("2m", "10s"). Empty means
	// the built-in bounds, which differ for a database and for an application.
	ReadyTimeout  string  `json:"ready_timeout,omitempty"`
	ReadyInterval string  `json:"ready_interval,omitempty"`
	Backup        *Backup `json:"backup,omitempty"`
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
// project to the next: four real projects gave four migration commands, three
// app service names and two ways of telling the app which database to use.
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

func Expand(s, database string) string {
	return strings.ReplaceAll(s, DatabasePlaceholder, database)
}
