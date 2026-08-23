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

func TestStepperFillsAProjectFromScratch(t *testing.T) {
	dir := repoWithCompose(t)
	var out bytes.Buffer
	in := strings.NewReader(strings.Join([]string{
		dir,                        // repository directory
		"main",                     // base branch
		"y",                        // enable the backup
		"",                         // database service, keep the default
		"",                         // database engine, keep the detected one
		"",                         // database user, keep the default
		"backend",                  // service running the migrations
		"python manage.py migrate", // migration command
		"",                         // no deps command
		"DB_NAME={{database}}",     // environment
		"",                         // end of the environment
		"y",                        // git-container
	}, "\n") + "\n")

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{})
	if err != nil {
		t.Fatalf("stepper: %v", err)
	}
	p, _ := u.Apply(config.Project{})

	if p.Dir != dir || p.BaseBranch != "main" || !p.Dump || !p.GitContainer {
		t.Fatalf("project = %+v", p)
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
	in := strings.NewReader(strings.Join([]string{
		"", "", "", "", "", "appuser", "", "", "", "", "",
	}, "\n") + "\n")

	u, err := runProjectStepper(newPrompter(in, new(bytes.Buffer)), current)
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
	in := strings.NewReader(strings.Join([]string{
		dir, "main", "y",
		"",        // database service
		"",        // database engine: keep the detected mysql
		"",        // database user
		"backend", // service running the migrations
		"migrate", // migration command
		"",        // deps
		"",        // environment end
		"n",       // git-container
	}, "\n") + "\n")

	u, err := runProjectStepper(newPrompter(in, new(bytes.Buffer)), config.Project{})
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
	in := strings.NewReader(strings.Join([]string{
		dir, "main", "y",
		"",        // database service
		"oracle",  // unknown engine
		"mariadb", // corrected
		"",        // database user
		"backend", "migrate", "", "", "n",
	}, "\n") + "\n")

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{})
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
	in := strings.NewReader(strings.Join([]string{
		dir, "main", "y",
		"",           // database service
		"sqlite",     // engine
		"../evil.db", // escaping file path, re-asked
		"var/app.db", // corrected
		"backend", "migrate", "", "", "n",
	}, "\n") + "\n")

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{})
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
	in := strings.NewReader(filepath.Join(dir, "nope") + "\n" + dir + "\nmain\nn\nn\n")

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{})
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
	a := &app{in: strings.NewReader(strings.Join([]string{
		dir, "main", "y",
		"",           // database service
		"",           // database engine, keep the detected one
		"",           // database user
		"my Backend", // invalid identifier for the migration service
		"migrate",    // migration command
		"",           // no deps command
		"",           // end of the environment
		"n",          // git-container
	}, "\n") + "\n"), out: new(bytes.Buffer)}
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
	in := strings.NewReader(plain + "\n" + repo + "\nmain\nn\nn\n")

	u, err := runProjectStepper(newPrompter(in, &out), config.Project{})
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
	if _, err := runProjectStepper(newPrompter(strings.NewReader(""), new(bytes.Buffer)), config.Project{}); err == nil {
		t.Fatal("a closed input should end the stepper")
	}
}
