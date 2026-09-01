package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dbengine"
)

// runProjectStepper asks for what a project needs one question at a time, each
// answer defaulting to what it already carries. inheritedBase is what applies
// while the project names no base of its own: default_base_branch, or fallback.
func runProjectStepper(p *prompter, current config.Project, inheritedBase string) (config.ProjectUpdate, error) {
	var u config.ProjectUpdate

	dir, err := askDir(p, current.Dir)
	if err != nil {
		return u, err
	}
	u.Dir = &dir

	if err := askBaseBranch(p, current.BaseBranch, inheritedBase, &u); err != nil {
		return u, err
	}

	dump, err := p.askYesNo("enable the database backup?", current.Dump)
	if err != nil {
		return u, err
	}
	u.Dump = &dump
	if dump {
		if err := askBackup(p, dir, current, &u); err != nil {
			return u, err
		}
	}

	// Asked outside the backup block: the dump carries the schema and never the
	// seed data, so a project seeds a fresh worktree whether it has one or not.
	postCreate, err := p.ask("command to run in the application container after a new worktree starts (e.g. manage.py seed_data)", current.PostCreate)
	if err != nil {
		return u, err
	}
	u.PostCreate = &postCreate

	gitContainer, err := p.askYesNo("create the .git-container symlinks? (only for projects bind-mounting the git-dir)", current.GitContainer)
	if err != nil {
		return u, err
	}
	u.GitContainer = &gitContainer
	return u, nil
}

// askBaseBranch records an answer only when one is typed. Writing the inherited
// branch back would pin the project to whatever default_base_branch said the day
// it was registered, which is how that setting came to apply to nobody.
func askBaseBranch(p *prompter, current, inherited string, u *config.ProjectUpdate) error {
	if current != "" {
		base, err := p.ask("base branch", current)
		if err != nil {
			return err
		}
		u.BaseBranch = &base
		return nil
	}
	base, err := p.askInherited("base branch", inherited)
	if err != nil {
		return err
	}
	if base != "" {
		u.BaseBranch = &base
	}
	return nil
}

// askBackup works on the project's raw backup section, not the defaulted view:
// an unset engine must fall back to what the compose image says, not to the
// postgres default.
func askBackup(p *prompter, dir string, project config.Project, u *config.ProjectUpdate) error {
	current := project.BackupConfig()
	if services, err := compose.Services(dir); err == nil && len(services) > 0 {
		p.logf("  services in the compose file: %s", strings.Join(services, ", "))
	}
	dbService, err := p.ask("  database service", or(current.DBService, config.DefaultDBService))
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
			dbPath, err = p.ask("  database file (relative to the project)", or(current.DBPath, config.DefaultDBPath))
			if err != nil {
				return err
			}
			if config.ValidateRelativePath("db_path", dbPath) == nil {
				break
			}
			p.logf("  must be a relative path inside the project")
		}
	} else {
		dbUser, err = p.ask("  database user", or(current.DBUser, config.DefaultDBUser))
		if err != nil {
			return err
		}
	}
	appService, err := p.askRequired("  service running the migrations (e.g. backend, api, php-nginx)", current.AppService)
	if err != nil {
		return err
	}
	migrate, err := p.askRequired("  migration command (e.g. python manage.py migrate)", current.MigrateCommand)
	if err != nil {
		return err
	}
	// The default matches Django, Prisma and MikroORM, and nothing else: a
	// project whose migrations live elsewhere would have its dump reported as
	// up to date forever, since no commit ever touches that pathspec.
	migrations, err := p.ask("  git pathspec of the migration files, used to spot a stale dump (e.g. db/migrate/*)", or(current.MigrationsPath, config.DefaultMigrationsPath))
	if err != nil {
		return err
	}
	deps, err := p.ask("  dependency install command, if the image does not carry them (e.g. poetry install --no-root)", current.DepsCommand)
	if err != nil {
		return err
	}
	env, err := p.askPairs("  environment telling the app which database to target (e.g. DB_NAME="+config.DatabasePlaceholder+")", current.Env)
	if err != nil {
		return err
	}
	u.DBService, u.DBEngine, u.AppService = &dbService, &engine, &appService
	if dbengine.IsFileBased(engine) {
		u.DBPath = &dbPath
	} else {
		u.DBUser = &dbUser
	}
	u.MigrateCommand, u.DepsCommand, u.Env = &migrate, &deps, env
	// Recorded only once it says something the default does not: writing the
	// default back would report a change on every project registered before
	// the question existed, and fill its entry with the value it already had.
	if migrations != config.DefaultMigrationsPath || recordedPath != "" {
		u.MigrationsPath = &migrations
	}
	return nil
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
		engine, err := p.ask("  database engine ("+strings.Join(dbengine.Names(), ", ")+")", or(current, detected))
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

func or(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
