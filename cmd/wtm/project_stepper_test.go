package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// repoWithCompose is what the stepper points at: a git repository, whose
// compose file it reads to offer the service names.
func repoWithCompose(t *testing.T) string {
	t.Helper()
	return repoWithComposeBody(t, "services:\n  db:\n    image: postgres\n  backend:\n    build: .\n")
}

// repoWithComposeBody builds the same repository around a given compose file.
// The .git entry is what makes it a project wtm accepts: it creates worktrees,
// so a directory without git is refused at registration.
func repoWithComposeBody(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stepperAnswers names each question the stepper asks, so a fixture states only
// what it answers and a new question renumbers nothing. An empty field is a
// plain enter, which keeps the offered default; a field holding a newline
// answers a question the stepper asks twice, rejecting then correcting.
type stepperAnswers struct {
	dir, base, dump                     string
	dbService, dbEngine, dbUser, dbPath string
	appService, migrate                 string
	migrationsPath, deps                string
	startDependencies                   string
	env                                 []string // pairs, the blank terminator is added
	postCreate, gitContainer            string
}

func (a stepperAnswers) reader() *strings.Reader {
	lines := []string{a.dir, a.base, a.dump}
	if a.dump != "n" {
		lines = append(lines, a.dbService, a.dbEngine)
		// A file-based engine is asked for the file instead of the user.
		if a.dbPath != "" {
			lines = append(lines, a.dbPath)
		} else {
			lines = append(lines, a.dbUser)
		}
		lines = append(lines, a.appService, a.migrate, a.migrationsPath, a.deps, a.startDependencies)
		lines = append(lines, a.env...)
		lines = append(lines, "")
	}
	lines = append(lines, a.postCreate, a.gitContainer)
	return strings.NewReader(strings.Join(lines, "\n") + "\n")
}

func TestStepperFillsAProjectFromScratch(t *testing.T) {
	dir := repoWithCompose(t)
	var out bytes.Buffer
	in := stepperAnswers{
		dir:          dir,
		base:         "main",
		dump:         "y",
		appService:   "backend",
		migrate:      "python manage.py migrate",
		env:          []string{"DB_NAME={{database}}"},
		postCreate:   "manage.py seed_data",
		gitContainer: "y",
	}.reader()

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{}, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	// An answered default is not a setting: the pathspec stays out of the
	// registry so a later change of default still reaches this project.
	if u.MigrationsPath != nil {
		t.Fatalf("migrations_path should not be recorded when the default is kept, got %q", *u.MigrationsPath)
	}
	p, _ := u.Apply(config.Project{})

	if p.Dir != dir || p.BaseBranch != "main" || !p.Dump || !p.GitContainer {
		t.Fatalf("project = %+v", p)
	}
	if p.PostCreate != "manage.py seed_data" {
		t.Fatalf("post_create = %q", p.PostCreate)
	}
	b := p.BackupConfig()
	if b.DBService != config.DefaultDBService || b.DBUser != config.DefaultDBUser {
		t.Fatalf("defaults should have been offered and kept: %+v", b)
	}
	if b.DBEngine != "postgres" {
		t.Fatalf("the engine detected from the compose image should be kept: %+v", b)
	}
	if b.AppService != "backend" || b.MigrateCommand != "python manage.py migrate" {
		t.Fatalf("backup = %+v", b)
	}
	if b.Env["DB_NAME"] != "{{database}}" {
		t.Fatalf("env = %v", b.Env)
	}
	// The services of the compose file are what makes the question answerable
	// without going to read the project.
	if !strings.Contains(out.String(), "db, backend") {
		t.Fatalf("the detected services should be shown:\n%s", out.String())
	}
}

// Editing is the same walk with the current values as defaults: enter through
// everything and only the answered field moves.
func TestStepperOnlyChangesWhatIsAnswered(t *testing.T) {
	dir := repoWithCompose(t)
	current := config.Project{
		Dir:             dir,
		BaseBranch:      "develop",
		Dump:            true,
		PortOffset:      1000,
		WorktreeIndices: map[string]int{"feat/x": 2},
		Backup: &config.Backup{
			DBService:      "db",
			DBUser:         "postgres",
			DBEngine:       "postgres",
			AppService:     "backend",
			MigrateCommand: "python manage.py migrate",
			Env:            map[string]string{"DB_NAME": "{{database}}"},
		},
	}
	in := stepperAnswers{dbUser: "appuser"}.reader()

	u, err := runProjectStepper(newPrompter(in, new(bytes.Buffer)), current, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	edited, changes := u.Apply(current)

	if len(changes) != 1 || changes[0].Field != "db_user" || changes[0].To != "appuser" {
		t.Fatalf("changes = %+v", changes)
	}
	if edited.PortOffset != 1000 || edited.WorktreeIndices["feat/x"] != 2 {
		t.Fatalf("offset and indices must survive: %+v", edited)
	}
	if edited.Backup.Env["DB_NAME"] != "{{database}}" {
		t.Fatalf("the environment must survive an empty answer: %v", edited.Backup.Env)
	}
}

// The engine question offers what the compose image says as its default, so
// registering a mysql project is a plain enter, not a thing to know.
func TestStepperDetectsTheEngineFromTheComposeImage(t *testing.T) {
	dir := repoWithComposeBody(t, "services:\n  db:\n    image: mysql:8.4\n  backend:\n    build: .\n")
	// The engine is left empty on purpose: the detected mysql is the default.
	in := stepperAnswers{dir: dir, base: "main", dump: "y",
		appService: "backend", migrate: "migrate", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, new(bytes.Buffer)), config.Project{}, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.DBEngine == nil || *u.DBEngine != "mysql" {
		t.Fatalf("engine = %q, the compose image says mysql", *u.DBEngine)
	}
}

// An engine wtm does not support must be re-asked, not recorded and discovered
// at the first refresh.
func TestStepperAsksAgainForAnUnknownEngine(t *testing.T) {
	dir := repoWithCompose(t)
	var out bytes.Buffer
	in := stepperAnswers{dir: dir, base: "main", dump: "y",
		dbEngine:   "oracle\nmariadb", // unknown, then corrected
		appService: "backend", migrate: "migrate", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{}, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.DBEngine == nil || *u.DBEngine != "mariadb" {
		t.Fatalf("engine = %v", u.DBEngine)
	}
	if !strings.Contains(out.String(), "unknown") {
		t.Fatalf("the rejection should be explained:\n%s", out.String())
	}
}

// sqlite has no database user: that question is replaced by the file's path,
// and an escaping answer is re-asked on the spot.
func TestStepperAsksForTheFileInsteadOfTheUserOnSQLite(t *testing.T) {
	dir := repoWithCompose(t)
	var out bytes.Buffer
	in := stepperAnswers{dir: dir, base: "main", dump: "y",
		dbEngine:   "sqlite",
		dbPath:     "../evil.db\nvar/app.db", // escaping, then corrected
		appService: "backend", migrate: "migrate", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{}, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.DBPath == nil || *u.DBPath != "var/app.db" {
		t.Fatalf("db_path = %v", u.DBPath)
	}
	if u.DBUser != nil {
		t.Fatalf("the database user must not be asked for sqlite, got %v", *u.DBUser)
	}
	if !strings.Contains(out.String(), "relative path") {
		t.Fatalf("the rejection should be explained:\n%s", out.String())
	}
	if strings.Contains(out.String(), "database user") {
		t.Fatalf("the user question should not appear:\n%s", out.String())
	}
}

// A mistyped path is caught while the user is still there to fix it.
func TestStepperAsksAgainForADirectoryThatDoesNotExist(t *testing.T) {
	dir := repoWithCompose(t)
	var out bytes.Buffer
	in := stepperAnswers{dir: filepath.Join(dir, "nope") + "\n" + dir, // missing, then corrected
		base: "main", dump: "n", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{}, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.Dir == nil || *u.Dir != dir {
		t.Fatalf("dir = %v", u.Dir)
	}
	if !strings.Contains(out.String(), "not an accessible directory") {
		t.Fatalf("the reason should be shown:\n%s", out.String())
	}
}

// A prompt answer cannot hold a newline, so the realistic invalid identifier
// is a space or an uppercase letter; the stepper must reject it the same way
// the flag path does, through the shared validateUpdate gate.
func TestStepperAnswersGetTheSameValidationAsFlags(t *testing.T) {
	dir := repoWithCompose(t)
	a := &app{cfg: &config.Config{}, out: new(bytes.Buffer), in: stepperAnswers{
		dir: dir, base: "main", dump: "y",
		appService: "my Backend", // invalid identifier for the migration service
		migrate:    "migrate", gitContainer: "n"}.reader()}
	f := &projectFlags{}
	_, err := f.steppedUpdate(a, config.Project{})
	if err == nil || !strings.Contains(err.Error(), "application service") {
		t.Fatalf("prompt answers must pass the flag path's validation, got %v", err)
	}
}

// A directory without git is a project wtm can do nothing with: it creates
// worktrees. Saying so at registration beats failing four steps later, once a
// refresh has already built an image and dumped a database.
func TestStepperRefusesADirectoryThatIsNotAGitRepository(t *testing.T) {
	plain := t.TempDir()
	repo := repoWithCompose(t)
	var out bytes.Buffer
	in := stepperAnswers{dir: plain + "\n" + repo, // not a repository, then corrected
		base: "main", dump: "n", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{}, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.Dir == nil || *u.Dir != repo {
		t.Fatalf("dir = %v, the git repository should have been kept", u.Dir)
	}
	if !strings.Contains(out.String(), "not a git repository") {
		t.Fatalf("the reason should be shown:\n%s", out.String())
	}
}

// Nothing to read means nothing to ask: the stepper stops instead of looping.
func TestStepperStopsWhenTheInputIsClosed(t *testing.T) {
	if _, err := runProjectStepper(newPrompter(strings.NewReader(""), new(bytes.Buffer)), config.Project{}, config.FallbackBaseBranch); err == nil {
		t.Fatal("a closed input should end the stepper")
	}
}

// A project that names no base branch of its own must keep inheriting: offering
// the inherited value as a default and recording it back pinned every project to
// whatever applied the day it was registered, leaving the setting for nobody.
func TestStepperKeepsTheBaseBranchInheritedWhenNothingIsTyped(t *testing.T) {
	dir := repoWithCompose(t)
	var out bytes.Buffer
	in := stepperAnswers{dir: dir, dump: "n", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{}, "main")
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.BaseBranch != nil {
		t.Fatalf("base_branch should stay unset, got %q", *u.BaseBranch)
	}
	// What applies has to be visible, or the empty answer is a guess.
	if !strings.Contains(out.String(), "inherited: main") {
		t.Fatalf("the inherited branch should be shown:\n%s", out.String())
	}
}

// Typing one still records it: inheriting is the default, not the only option.
func TestStepperRecordsATypedBaseBranch(t *testing.T) {
	dir := repoWithCompose(t)
	in := stepperAnswers{dir: dir, base: "release", dump: "n", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, new(bytes.Buffer)), config.Project{}, "main")
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.BaseBranch == nil || *u.BaseBranch != "release" {
		t.Fatalf("base_branch = %v", u.BaseBranch)
	}
}

// The pathspec decides whether a dump is reported as stale, and the default
// matches Django, Prisma and MikroORM alone: a Rails or Flyway layout had no
// way in but config.json.
func TestStepperRecordsAMigrationsPathThatDiffersFromTheDefault(t *testing.T) {
	dir := repoWithCompose(t)
	in := stepperAnswers{dir: dir, base: "main", dump: "y",
		appService: "backend", migrate: "rails db:migrate",
		migrationsPath: "db/migrate/*", gitContainer: "n"}.reader()

	u, err := runProjectStepper(newPrompter(in, new(bytes.Buffer)), config.Project{}, config.FallbackBaseBranch)
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	if u.MigrationsPath == nil || *u.MigrationsPath != "db/migrate/*" {
		t.Fatalf("migrations_path = %v", u.MigrationsPath)
	}
	p, _ := u.Apply(config.Project{})
	if p.BackupConfig().MigrationsPath != "db/migrate/*" {
		t.Fatalf("backup = %+v", p.BackupConfig())
	}
}
