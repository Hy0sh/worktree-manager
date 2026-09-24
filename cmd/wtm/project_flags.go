package main

import (
	"cmp"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
)

// projectFlags are the settings of a project as the command line carries them.
// create and edit share them: the same project has the same fields whether it
// is being registered or amended.
type projectFlags struct {
	dir           string
	base          string
	dump          bool
	gitContainer  bool
	dbService     string
	dbUser        string
	dbEngine      string
	dbPath        string
	appService    string
	deps          string
	migrate       string
	startDeps     bool
	migrations    string
	postCreate    string
	readyTimeout  string
	readyInterval string
	env           []string
	profiles      []string
	profileDescs  []string
	noInput       bool
}

func (f *projectFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.dir, "dir", "", "path to the repository")
	cmd.Flags().StringVar(&f.base, "base", "", "project's base branch")
	cmd.Flags().BoolVar(&f.dump, "dump", false, "enables the database backup for this project")
	cmd.Flags().BoolVar(&f.gitContainer, "git-container", false, "creates the .git-container symlinks (projects that bind-mount the git-dir)")
	cmd.Flags().StringVar(&f.dbService, "db-service", "", "compose service for the database (default: "+config.DefaultDBService+")")
	cmd.Flags().StringVar(&f.dbUser, "db-user", "", "database user (default: "+config.DefaultDBUser+")")
	cmd.Flags().StringVar(&f.dbEngine, "db-engine", "", "database engine: "+strings.Join(dbengine.Names(), ", ")+" (default: detected from the compose image, else "+config.DefaultDBEngine+")")
	cmd.Flags().StringVar(&f.dbPath, "db-path", "", "database file for file-based engines, relative to the project (default: "+config.DefaultDBPath+")")
	cmd.Flags().StringVar(&f.appService, "app-service", "", "compose service that runs the migrations (e.g. backend, api, php-nginx)")
	cmd.Flags().StringVar(&f.deps, "deps", "", "dependency install command before migration (e.g. 'poetry install --no-root --with dev')")
	cmd.Flags().StringVar(&f.migrate, "migrate", "", "migration command (e.g. 'python manage.py migrate', 'npx prisma migrate deploy')")
	cmd.Flags().BoolVar(&f.startDeps, "start-dependencies", false, "brings up what the application service depends_on while the backup refreshes, for a migration command that needs more than the database (object storage, a cache, a search index)")
	cmd.Flags().StringVar(&f.migrations, "migrations-path", "", "git pathspec of the migration files, used to spot a stale dump (default: "+config.DefaultMigrationsPath+", which matches Django, Prisma and MikroORM)")
	cmd.Flags().StringVar(&f.postCreate, "post-create", "", "command run in the application container after a new worktree starts (e.g. 'python manage.py seed_data')")
	cmd.Flags().StringVar(&f.readyTimeout, "ready-timeout", "", "how long a service may take to answer before post_create runs, e.g. 2m (default: 1m for the database, 10m for the application)")
	cmd.Flags().StringVar(&f.readyInterval, "ready-interval", "", "how often it is asked, e.g. 10s (default: 1s)")
	cmd.Flags().StringArrayVar(&f.env, "env", nil, "variable passed to the migration container, repeatable, replaces the whole set (e.g. --env DB_NAME="+config.DatabasePlaceholder+"); a value given here shows in `ps` and the shell history, the stepper keeps it off both")
	cmd.Flags().StringArrayVar(&f.profiles, "profile-set", nil, "subset of compose services `wtm start --profile` may bring up, repeatable, replaces the whole set (e.g. --profile-set light=db,backend)")
	cmd.Flags().StringArrayVar(&f.profileDescs, "profile-description", nil, "when to pick a profile, shown by `wtm project profiles`, repeatable, replaces the whole set (e.g. --profile-description 'async=Celery tasks')")
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
	if changed("start-dependencies") {
		u.StartDependencies = &f.startDeps
	}
	for _, pair := range []struct {
		name  string
		value *string
		into  **string
	}{
		{"db-service", &f.dbService, &u.DBService},
		{"db-user", &f.dbUser, &u.DBUser},
		{"db-engine", &f.dbEngine, &u.DBEngine},
		{"db-path", &f.dbPath, &u.DBPath},
		{"app-service", &f.appService, &u.AppService},
		{"deps", &f.deps, &u.DepsCommand},
		{"migrate", &f.migrate, &u.MigrateCommand},
		{"migrations-path", &f.migrations, &u.MigrationsPath},
		{"post-create", &f.postCreate, &u.PostCreate},
		{"ready-timeout", &f.readyTimeout, &u.ReadyTimeout},
		{"ready-interval", &f.readyInterval, &u.ReadyInterval},
	} {
		if changed(pair.name) {
			*pair.into = pair.value
		}
	}
	if changed("env") {
		env, err := parsePairs("--env", "KEY=VALUE", f.env)
		if err != nil {
			return u, err
		}
		u.Env = env
	}
	if changed("profile-set") {
		profiles, err := parseProfiles(f.profiles)
		if err != nil {
			return u, err
		}
		u.Profiles = profiles
	}
	if changed("profile-description") {
		descs, err := parsePairs("--profile-description", "NAME=text", f.profileDescs)
		if err != nil {
			return u, err
		}
		u.ProfileDescriptions = descs
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
	if u.DBEngine != nil && *u.DBEngine != "" && !dbengine.Valid(*u.DBEngine) {
		return fmt.Errorf("unknown database engine %q (supported: %s)", *u.DBEngine, strings.Join(dbengine.Names(), ", "))
	}
	if u.DBPath != nil && *u.DBPath != "" {
		if err := config.ValidateRelativePath("db_path", *u.DBPath); err != nil {
			return err
		}
	}
	// A create must not discover a malformed duration halfway through, once the
	// worktree and its stack are already up.
	for flag, value := range map[string]*string{
		"ready-timeout":  u.ReadyTimeout,
		"ready-interval": u.ReadyInterval,
	} {
		if value == nil || *value == "" {
			continue
		}
		d, err := time.ParseDuration(*value)
		if err != nil {
			return fmt.Errorf("--%s %q is not a duration (e.g. 90s, 2m, 1h30m)", flag, *value)
		}
		if d <= 0 {
			return fmt.Errorf("--%s must be positive, got %s", flag, d)
		}
	}
	return nil
}

// detectEngineIfUnset fills db_engine from the compose image when the backup is
// on and no engine was given: the stepper offers the detection as its default,
// and a scripted --no-input call deserves the same rather than a silent postgres.
func detectEngineIfUnset(p *config.Project, logf func(string, ...any)) {
	if !p.Dump || p.Dir == "" || (p.Backup != nil && p.Backup.DBEngine != "") {
		return
	}
	img, ok := compose.ServiceImage(p.Dir, p.BackupConfig().DBService)
	if !ok {
		return
	}
	eng, ok := dbengine.Detect(img)
	if !ok {
		// Recording postgres here would only fail at the first refresh, with
		// pg_dump against whatever this image really is.
		logf("warning: cannot tell the database engine from image %q, postgres will be assumed; set it with `wtm project edit --db-engine <engine>` if that is wrong", img)
		return
	}
	b := config.Backup{}
	if p.Backup != nil {
		b = *p.Backup
	}
	b.DBEngine = eng.Name()
	p.Backup = &b
}

// warnPinnedContainers reports the services whose compose file fixes their
// container_name. It is the one isolation wtm cannot provide: ports, volumes and
// the project name are rebased, so the main and worktree stacks collide there.
func warnPinnedContainers(p config.Project, logf func(string, ...any)) {
	if p.Dir == "" {
		return
	}
	pinned, err := compose.PinnedContainerNames(p.Dir)
	if err != nil || len(pinned) == 0 {
		return
	}
	logf("warning: %s pin a container_name, which wtm cannot rebase: the main stack and a worktree stack "+
		"cannot run at the same time until those lines go", strings.Join(pinned, ", "))
}

// steppedUpdate walks the questions, unless the command was told not to ask,
// then applies the same gate the flag path gets in update(): answers typed at
// the prompt land in generated files too.
func (f *projectFlags) steppedUpdate(a *app, p *prompter, current config.Project) (config.ProjectUpdate, error) {
	if f.noInput {
		return config.ProjectUpdate{}, fmt.Errorf("nothing to do: pass the settings as flags, or drop --no-input to be asked")
	}
	u, err := runProjectStepper(a.runner, p, current, a.cfg.BaseBranchFor(config.Project{}))
	if err != nil {
		return u, err
	}
	return u, validateUpdate(u)
}

// confirmChanges shows what the answers amount to before anything is written:
// a dozen questions in, one wrong enter is easier to catch in a table.
func confirmChanges(p *prompter, changes []config.FieldChange, withFrom bool) (bool, error) {
	p.logf("\nSummary")
	writeChanges(p.out, changes, withFrom)
	return p.askYesNo("save?", true)
}

// printChanges reports what the edit did, field by field, so adding a backup
// to a project configured months ago is auditable at a glance.
func printChanges(a *app, name string, changes []config.FieldChange) {
	if len(changes) == 0 {
		fmt.Fprintf(a.out, "project %s unchanged\n", name)
		return
	}
	fmt.Fprintf(a.out, "project %s updated\n", name)
	writeChanges(a.out, changes, true)
}

// writeChanges leaves the old value out for a registration, where it is
// always unset.
func writeChanges(out io.Writer, changes []config.FieldChange, withFrom bool) {
	width := 0
	for _, c := range changes {
		if len(c.Field) > width {
			width = len(c.Field)
		}
	}
	for _, c := range changes {
		if withFrom {
			fmt.Fprintf(out, "  %-*s  %s -> %s\n", width, c.Field, cmp.Or(c.From, "(unset)"), cmp.Or(c.To, "(unset)"))
		} else {
			fmt.Fprintf(out, "  %-*s  %s\n", width, c.Field, c.To)
		}
	}
}
