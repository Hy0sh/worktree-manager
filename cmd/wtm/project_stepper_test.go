package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// repoWithCompose is what the stepper points at: an existing directory, whose
// compose file it reads to offer the service names.
func repoWithCompose(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := "services:\n  db:\n    image: postgres\n  backend:\n    build: .\n"
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
		"",                         // postgres user, keep the default
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
			AppService:     "backend",
			MigrateCommand: "python manage.py migrate",
			Env:            map[string]string{"DB_NAME": "{{database}}"},
		},
	}
	in := strings.NewReader(strings.Join([]string{
		"", "", "", "", "appuser", "", "", "", "", "",
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

// Nothing to read means nothing to ask: the stepper stops instead of looping.
func TestStepperStopsWhenTheInputIsClosed(t *testing.T) {
	if _, err := runProjectStepper(newPrompter(strings.NewReader(""), new(bytes.Buffer)), config.Project{}); err == nil {
		t.Fatal("a closed input should end the stepper")
	}
}
