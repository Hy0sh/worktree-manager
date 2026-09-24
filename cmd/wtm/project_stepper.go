package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
)

// runProjectStepper asks for what a project needs one question at a time, each
// answer defaulting to what it already carries. inheritedBase is what applies
// while the project names no base of its own: default_base_branch, or fallback.
// runner checks answers against the repository; nil skips those checks.
func runProjectStepper(runner execx.Runner, p *prompter, current config.Project, inheritedBase string) (config.ProjectUpdate, error) {
	var u config.ProjectUpdate

	p.logf("Repository")
	dirDefault := current.Dir
	if dirDefault == "" && runner != nil {
		dirDefault, _ = gitx.RepoRoot(context.Background(), runner)
	}
	dir, err := askDir(p, dirDefault)
	if err != nil {
		return u, err
	}
	u.Dir = &dir

	if err := askBaseBranch(p, localBranches(runner, dir), current.BaseBranch, inheritedBase, &u); err != nil {
		return u, err
	}

	p.logf("\nDatabase backup")
	p.explain("wtm migrates a throwaway database once and dumps it, so each new worktree",
		"restores that dump in seconds instead of replaying every migration")
	dump, err := p.askYesNo("enable the database backup?", current.Dump)
	if err != nil {
		return u, err
	}
	u.Dump = &dump
	if dump {
		if err := askBackup(p, runner, dir, current, &u); err != nil {
			return u, err
		}
	}

	// Asked outside the backup block: the dump carries the schema and never the
	// seed data, so a project seeds a fresh worktree whether it has one or not.
	p.logf("\nNew worktrees")
	postCreate, err := p.ask("command to run in the application container after a new worktree starts, empty for none (e.g. manage.py seed_data)", current.PostCreate)
	if err != nil {
		return u, err
	}
	u.PostCreate = &postCreate

	// Nobody can answer this one without knowing VirtioFS, and the compose file
	// already knows: it is asked when a volume mounts the git-dir, or to turn
	// the setting off.
	mount, mounted := compose.GitDirMount(dir)
	if !mounted && !current.GitContainer {
		return u, nil
	}
	if mounted {
		p.explain("your compose file mounts "+mount+": in a worktree .git is a pointer file",
			"Docker on macOS cannot mount, so wtm links .git-container to the real git-dir")
	}
	gitContainer, err := p.askYesNo("create the .git-container link?", current.GitContainer)
	if err != nil {
		return u, err
	}
	u.GitContainer = &gitContainer
	return u, nil
}

// askBaseBranch records an answer only when one is typed. Writing the inherited
// branch back would pin the project to whatever default_base_branch said the day
// it was registered, which is how that setting came to apply to nobody.
func askBaseBranch(p *prompter, branches []string, current, inherited string, u *config.ProjectUpdate) error {
	question := "base branch"
	if len(branches) > 0 {
		question += " (" + strings.Join(branches, ", ") + ")"
	}
	if current != "" {
		base, err := p.ask(question, current)
		if err != nil {
			return err
		}
		u.BaseBranch = &base
		return nil
	}
	base, err := p.askInherited(question, inherited)
	if err != nil {
		return err
	}
	if base != "" {
		u.BaseBranch = &base
	}
	return nil
}

// askBackup only runs when the backup is on: a migration command a project will
// never run is noise. It reads the raw backup section, not the defaulted view,
// so an unset engine falls back to the compose image and not to postgres.
func askBackup(p *prompter, runner execx.Runner, dir string, project config.Project, u *config.ProjectUpdate) error {
	current := project.BackupConfig()
	dbDefault := current.DBService
	services, _ := compose.Services(dir)
	// A project that never named its service gets the one running a database
	// image, rather than a "db" its compose file may not have.
	if project.Backup == nil || project.Backup.DBService == "" {
		dbDefault = cmp.Or(databaseService(dir, services), dbDefault)
	}
	dbService, err := askService(p, "  database service", services, dbDefault)
	if err != nil {
		return err
	}
	recorded, recordedPath := "", ""
	if project.Backup != nil {
		recorded, recordedPath = project.Backup.DBEngine, project.Backup.MigrationsPath
	}
	engine, err := askEngine(p, dir, dbService, recorded)
	if err != nil {
		return err
	}
	// A file-based engine has no database user; what it needs instead is
	// where the file lives, so the dump lands where the application looks.
	var dbUser, dbPath string
	if dbengine.IsFileBased(engine) {
		for {
			dbPath, err = p.ask("  database file (relative to the project)", cmp.Or(current.DBPath, config.DefaultDBPath))
			if err != nil {
				return err
			}
			if config.ValidateRelativePath("db_path", dbPath) == nil {
				break
			}
			p.logf("  must be a relative path inside the project")
		}
	} else if engine == config.DefaultDBEngine {
		// Postgres alone takes a user: the other engines connect as the root
		// their image sets up. POSTGRES_USER is what that image creates.
		userDefault := current.DBUser
		if user := compose.ServiceEnvironment(dir, dbService)["POSTGRES_USER"]; user != "" && !strings.Contains(user, "${") &&
			(project.Backup == nil || project.Backup.DBUser == "") {
			userDefault = user
		}
		dbUser, err = p.ask("  database user", userDefault)
		if err != nil {
			return err
		}
	}
	appQuestion := "  service running the migrations"
	if len(services) == 0 {
		appQuestion += " (e.g. backend, api, php-nginx)"
	}
	// The one service built from the repository runs its code, migrations
	// included; with several, picking one would be a guess.
	appDefault := current.AppService
	if built := compose.BuiltServices(dir); appDefault == "" && len(built) == 1 {
		appDefault = built[0]
	}
	appService, err := askService(p, appQuestion, slices.DeleteFunc(slices.Clone(services), func(s string) bool { return s == dbService }), appDefault)
	if err != nil {
		return err
	}
	suggestedMigrate, suggestedPath := suggestMigrations(dir)
	migrateQuestion := "  migration command"
	if suggestedMigrate == "" {
		migrateQuestion += " (e.g. python manage.py migrate)"
	}
	migrate, err := p.askRequired(migrateQuestion, cmp.Or(current.MigrateCommand, suggestedMigrate))
	if err != nil {
		return err
	}
	// The default matches Django, Prisma and MikroORM, and nothing else: a
	// project whose migrations live elsewhere would have its dump reported as
	// up to date forever, since no commit ever touches that pathspec.
	p.explain("only read to tell when the dump falls behind: a commit touching these files makes it stale",
		"the default covers any migrations/ directory (Django, Doctrine, Laravel, Prisma, MikroORM)",
		"no migration files, the schema synced from the code? name what defines it (e.g. src/Entity/*)")
	migrations, err := askMigrationsPath(p, runner, dir, cmp.Or(recordedPath, suggestedPath, config.DefaultMigrationsPath))
	if err != nil {
		return err
	}
	deps, err := p.ask("  dependency install command, empty if the image already carries them (e.g. poetry install --no-root)", current.DepsCommand)
	if err != nil {
		return err
	}
	// Off for a plain migration, which talks to the database alone. A command
	// that also seeds may reach for object storage, a cache or a search index.
	// A service depending on nothing else has nothing to start: not asked.
	var startDeps *bool
	if others := otherDependencies(dir, appService, dbService); len(others) > 0 || current.StartDependencies {
		p.explain("the migration runs in a throwaway container with the database alone",
			"answer y only if it also reaches a cache, object storage or a search index, as fixtures may")
		if len(others) > 0 {
			p.explain(appService + " also depends on " + strings.Join(others, ", "))
		}
		answer, err := p.askYesNo("  does the migration command need services besides the database?", current.StartDependencies)
		if err != nil {
			return err
		}
		startDeps = &answer
	}
	p.explain("the migration targets a temporary database whose name wtm picks: "+config.DatabasePlaceholder+" stands for it",
		"set the variable your application reads its database from, e.g.",
		"  DB_NAME="+config.DatabasePlaceholder,
		"  DATABASE_URL=postgresql://user:pass@db:5432/"+config.DatabasePlaceholder)
	env, err := p.askPairs("  variables pointing the migration at that database", current.Env, suggestEnv(dir, appService))
	if err != nil {
		return err
	}
	u.DBService, u.DBEngine, u.AppService = &dbService, &engine, &appService
	if dbengine.IsFileBased(engine) {
		u.DBPath = &dbPath
	} else if engine == config.DefaultDBEngine {
		u.DBUser = &dbUser
	}
	u.MigrateCommand, u.DepsCommand, u.Env = &migrate, &deps, env
	u.StartDependencies = startDeps
	// Recorded only once it says something the default does not: writing the
	// default back would report a change on every project registered before
	// the question existed, and fill its entry with the value it already had.
	if migrations != config.DefaultMigrationsPath || recordedPath != "" {
		u.MigrationsPath = &migrations
	}
	return nil
}

// askMigrationsPath warns about a pathspec that matches no tracked file: no
// commit would ever touch it, so the dump would read as up to date forever.
// Typing it again keeps it, for migrations not committed yet.
func askMigrationsPath(p *prompter, runner execx.Runner, dir, current string) (string, error) {
	warned := ""
	for {
		answer, err := p.ask("  git pathspec of the migration files (e.g. db/migrate/* for Rails)", current)
		if err != nil || runner == nil || answer == warned {
			return answer, err
		}
		if _, err := runner.Run(context.Background(), execx.Cmd{
			Name: "git",
			Args: []string{"-C", dir, "ls-files", "--error-unmatch", "--", answer},
		}); err == nil {
			return answer, nil
		}
		p.logf("  %s matches no file git tracks: the dump would never be reported stale; enter to keep it anyway", answer)
		warned, current = answer, answer
	}
}

// askService lists the compose services to pick from, and flags an answer that
// is none of them. Typing it again keeps it: a service may come from a file
// wtm does not read, through include or -f.
func askService(p *prompter, question string, services []string, current string) (string, error) {
	if len(services) > 0 {
		question += " (" + strings.Join(services, ", ") + ")"
	}
	warned := ""
	for {
		answer, err := p.askRequired(question, current)
		if err != nil || len(services) == 0 || answer == warned || slices.Contains(services, answer) {
			return answer, err
		}
		p.logf("  %s is not a service of the compose file; enter to keep it anyway", answer)
		warned, current = answer, answer
	}
}

// databaseService is the first service whose image is an engine wtm knows,
// "" when none is.
func databaseService(dir string, services []string) string {
	for _, s := range services {
		if img, ok := compose.ServiceImage(dir, s); ok {
			if _, ok := dbengine.Detect(img); ok {
				return s
			}
		}
	}
	return ""
}

// askEngine offers what the compose image of the database service says as the
// default, so registering a mysql project is a plain enter. An unsupported one
// is re-asked on the spot rather than discovered at the first refresh.
func askEngine(p *prompter, dir, dbService, current string) (string, error) {
	detected := config.DefaultDBEngine
	if img, ok := compose.ServiceImage(dir, dbService); ok {
		if eng, ok := dbengine.Detect(img); ok {
			detected = eng.Name()
		}
	}
	for {
		engine, err := p.ask("  database engine ("+strings.Join(dbengine.Names(), ", ")+")", cmp.Or(current, detected))
		if err != nil {
			return "", err
		}
		if dbengine.Valid(engine) {
			return engine, nil
		}
		p.logf("  unknown engine %q", engine)
	}
}

// askName asks for the name every other command uses. It is the key of the
// registry rather than a field of the project, which is why the stepper does
// not ask it: the directory it defaults to has to be known first.
func askName(p *prompter, current string) (string, error) {
	if config.ValidateIdentifier("project name", current) != nil {
		current = "" // a directory whose name would not do as a project name
	}
	for {
		answer, err := p.askRequired("project name", current)
		if err != nil {
			return "", err
		}
		err = config.ValidateIdentifier("project name", answer)
		if err == nil {
			return answer, nil
		}
		p.logf("  %v", err)
		current = ""
	}
}

// askDir keeps asking until the answer is a directory that exists: a typo
// caught here costs one line, caught at the first `wtm create` it costs a
// puzzled minute.
func askDir(p *prompter, current string) (string, error) {
	for {
		answer, err := p.askRequired("repository directory", current)
		if err != nil {
			return "", err
		}
		dir, err := projectDir(answer)
		if err == nil {
			return dir, nil
		}
		p.logf("  %v", err)
		current = ""
	}
}

// projectDir turns what was typed into the absolute path wtm records. A shell
// expands ~ before wtm ever sees it; a prompt does not, so it is done here.
func projectDir(input string) (string, error) {
	if input == "~" || strings.HasPrefix(input, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory not found: %w", err)
		}
		input = filepath.Join(home, strings.TrimPrefix(input, "~"))
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not an accessible directory", abs)
	}
	// A directory that is not a repository would fail four steps later, once a
	// refresh has built an image. The entry is a file in a linked worktree and a
	// directory in a main repository, hence Stat rather than IsDir.
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return "", fmt.Errorf("%s is not a git repository: wtm creates worktrees, which git alone can do", abs)
	}
	return abs, nil
}
