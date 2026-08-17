package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// projectFlags are the settings of a project as the command line carries them.
// create and edit share them: the same project has the same fields whether it
// is being registered or amended.
type projectFlags struct {
	dir          string
	base         string
	dump         bool
	gitContainer bool
	dbService    string
	dbUser       string
	appService   string
	deps         string
	migrate      string
	env          []string
	noInput      bool
}

func (f *projectFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.dir, "dir", "", "path to the repository")
	cmd.Flags().StringVar(&f.base, "base", "", "project's base branch")
	cmd.Flags().BoolVar(&f.dump, "dump", false, "enables the Postgres backup for this project")
	cmd.Flags().BoolVar(&f.gitContainer, "git-container", false, "creates the .git-container symlinks (projects that bind-mount the git-dir)")
	cmd.Flags().StringVar(&f.dbService, "db-service", "", "compose service for the database (default: "+config.DefaultDBService+")")
	cmd.Flags().StringVar(&f.dbUser, "db-user", "", "postgres user (default: "+config.DefaultDBUser+")")
	cmd.Flags().StringVar(&f.appService, "app-service", "", "compose service that runs the migrations (e.g. backend, api, php-nginx)")
	cmd.Flags().StringVar(&f.deps, "deps", "", "dependency install command before migration (e.g. 'poetry install --no-root --with dev')")
	cmd.Flags().StringVar(&f.migrate, "migrate", "", "migration command (e.g. 'python manage.py migrate', 'npx prisma migrate deploy')")
	cmd.Flags().StringArrayVar(&f.env, "env", nil, "variable passed to the migration container, repeatable, replaces the whole set (e.g. --env DB_NAME="+config.DatabasePlaceholder+")")
	cmd.Flags().BoolVar(&f.noInput, "no-input", false, "fail instead of asking, for scripts and CI")
}

// update reads only the flags actually typed. Cobra cannot tell an unset
// --dump from an explicit --dump=false, and an edit must not flip a setting
// the command line never mentioned.
func (f *projectFlags) update(cmd *cobra.Command) (config.ProjectUpdate, error) {
	var u config.ProjectUpdate
	changed := func(name string) bool { return cmd.Flags().Changed(name) }

	if changed("dir") {
		dir, err := projectDir(f.dir)
		if err != nil {
			return u, err
		}
		u.Dir = &dir
	}
	if changed("base") {
		u.BaseBranch = &f.base
	}
	if changed("dump") {
		u.Dump = &f.dump
	}
	if changed("git-container") {
		u.GitContainer = &f.gitContainer
	}
	for _, pair := range []struct {
		name  string
		value *string
		into  **string
	}{
		{"db-service", &f.dbService, &u.DBService},
		{"db-user", &f.dbUser, &u.DBUser},
		{"app-service", &f.appService, &u.AppService},
		{"deps", &f.deps, &u.DepsCommand},
		{"migrate", &f.migrate, &u.MigrateCommand},
	} {
		if changed(pair.name) {
			*pair.into = pair.value
		}
	}
	if changed("env") {
		env, err := parseEnv(f.env)
		if err != nil {
			return u, err
		}
		u.Env = env
	}
	return u, validateUpdate(u)
}

// validateUpdate rejects what would break a generated file: both names end up
// in a compose document, one of them inside a shell script.
func validateUpdate(u config.ProjectUpdate) error {
	for kind, value := range map[string]*string{
		"database service":    u.DBService,
		"application service": u.AppService,
	} {
		if value == nil || *value == "" {
			continue
		}
		if err := config.ValidateIdentifier(kind, *value); err != nil {
			return err
		}
	}
	return nil
}

// stepper walks the questions, unless the command was told not to ask.
func (f *projectFlags) stepper(a *app, current config.Project) (config.ProjectUpdate, error) {
	if f.noInput {
		return config.ProjectUpdate{}, fmt.Errorf("nothing to do: pass the settings as flags, or drop --no-input to be asked")
	}
	return runProjectStepper(newPrompter(a.in, a.out), current)
}

// printChanges reports what the edit did, field by field, so adding a backup
// to a project configured months ago is auditable at a glance.
func printChanges(a *app, name string, changes []config.FieldChange) {
	if len(changes) == 0 {
		fmt.Fprintf(a.out, "project %s unchanged\n", name)
		return
	}
	fmt.Fprintf(a.out, "project %s updated\n", name)
	width := 0
	for _, c := range changes {
		if len(c.Field) > width {
			width = len(c.Field)
		}
	}
	for _, c := range changes {
		fmt.Fprintf(a.out, "  %-*s  %s -> %s\n", width, c.Field, unsetOr(c.From), unsetOr(c.To))
	}
}

func unsetOr(value string) string {
	if value == "" {
		return "(unset)"
	}
	return value
}
