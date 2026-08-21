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

// runProjectStepper asks for what a project needs, one question at a time,
// rather than expecting the ten flags of `project create` to be known in
// advance. Every answer defaults to what the project already carries, so an
// edit is a series of empty lines plus the one field being changed.
func runProjectStepper(p *prompter, current config.Project) (config.ProjectUpdate, error) {
	var u config.ProjectUpdate

	dir, err := askDir(p, current.Dir)
	if err != nil {
		return u, err
	}
	u.Dir = &dir

	base, err := p.ask("base branch", or(current.BaseBranch, config.FallbackBaseBranch))
	if err != nil {
		return u, err
	}
	u.BaseBranch = &base

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

	gitContainer, err := p.askYesNo("create the .git-container symlinks? (only for projects bind-mounting the git-dir)", current.GitContainer)
	if err != nil {
		return u, err
	}
	u.GitContainer = &gitContainer
	return u, nil
}

// askBackup only runs when the backup is on: asking for a migration command a
// project will never run is noise. It works on the project's raw backup
// section, not the defaulted view: an unset engine must fall back to what the
// compose image says, not to the postgres default.
func askBackup(p *prompter, dir string, project config.Project, u *config.ProjectUpdate) error {
	current := project.BackupConfig()
	if services, err := compose.Services(dir); err == nil && len(services) > 0 {
		p.logf("  services in the compose file: %s", strings.Join(services, ", "))
	}
	dbService, err := p.ask("  database service", or(current.DBService, config.DefaultDBService))
	if err != nil {
		return err
	}
	recorded := ""
	if project.Backup != nil {
		recorded = project.Backup.DBEngine
	}
	engine, err := askEngine(p, dir, dbService, recorded)
	if err != nil {
		return err
	}
	dbUser, err := p.ask("  database user", or(current.DBUser, config.DefaultDBUser))
	if err != nil {
		return err
	}
	appService, err := p.askRequired("  service running the migrations", current.AppService)
	if err != nil {
		return err
	}
	migrate, err := p.askRequired("  migration command", current.MigrateCommand)
	if err != nil {
		return err
	}
	deps, err := p.ask("  dependency install command, if the image does not carry them", current.DepsCommand)
	if err != nil {
		return err
	}
	env, err := p.askPairs("  environment telling the app which database to target", current.Env)
	if err != nil {
		return err
	}
	u.DBService, u.DBEngine, u.DBUser, u.AppService = &dbService, &engine, &dbUser, &appService
	u.MigrateCommand, u.DepsCommand, u.Env = &migrate, &deps, env
	return nil
}

// askEngine offers what the compose image of the database service says as the
// default, so registering a mysql project is a plain enter. An engine wtm does
// not support is re-asked on the spot rather than discovered at the first
// refresh.
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
		if _, err := dbengine.ByName(engine); err == nil {
			return engine, nil
		}
		p.logf("  unknown engine %q", engine)
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
	return abs, nil
}

func or(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
