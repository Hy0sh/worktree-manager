package config

import "testing"

func str(s string) *string { return &s }
func flag(b bool) *bool    { return &b }

// The port offset and the recorded indices are what keep running stacks
// reachable: an edit that resets them would rename every compose project.
func TestApplyLeavesUntouchedFieldsAlone(t *testing.T) {
	p := Project{
		Dir:             "/repo/my-app",
		BaseBranch:      "develop",
		PortOffset:      3,
		WorktreeIndices: map[string]int{"feat/x": 2},
	}
	edited, changes := ProjectUpdate{Dump: flag(true)}.Apply(p)

	if edited.PortOffset != 3 || edited.WorktreeIndices["feat/x"] != 2 {
		t.Fatalf("offset and indices must survive an edit: %+v", edited)
	}
	if edited.Dir != "/repo/my-app" || edited.BaseBranch != "develop" {
		t.Fatalf("unnamed fields must survive: %+v", edited)
	}
	if len(changes) != 1 || changes[0].Field != "dump" || changes[0].To != "true" {
		t.Fatalf("changes = %+v", changes)
	}
}

// Adding the backup to a project registered without one is the case this
// command exists for.
func TestApplyCreatesTheBackupSectionOnDemand(t *testing.T) {
	p := Project{Dir: "/repo/my-app"}
	edited, changes := ProjectUpdate{
		Dump:           flag(true),
		AppService:     str("backend"),
		MigrateCommand: str("python manage.py migrate"),
		Env:            map[string]string{"DB_NAME": "{{database}}"},
	}.Apply(p)

	if edited.Backup == nil {
		t.Fatal("the backup section should have been created")
	}
	if edited.Backup.AppService != "backend" {
		t.Fatalf("backup = %+v", edited.Backup)
	}
	if edited.Backup.Env["DB_NAME"] != "{{database}}" {
		t.Fatalf("env = %v", edited.Backup.Env)
	}
	if len(changes) != 4 {
		t.Fatalf("every filled field should be reported, got %+v", changes)
	}
}

func TestApplyKeepsTheBackupFieldsItDoesNotName(t *testing.T) {
	p := Project{Backup: &Backup{
		DBService:      "postgres",
		AppService:     "api",
		MigrateCommand: "npx prisma migrate deploy",
	}}
	edited, changes := ProjectUpdate{AppService: str("backend")}.Apply(p)

	if edited.Backup.DBService != "postgres" || edited.Backup.MigrateCommand != "npx prisma migrate deploy" {
		t.Fatalf("the other backup fields must survive: %+v", edited.Backup)
	}
	if len(changes) != 1 || changes[0].From != "api" || changes[0].To != "backend" {
		t.Fatalf("changes = %+v", changes)
	}
}

// Re-running the same edit is a no-op, and the command says so instead of
// printing a diff of nothing.
func TestApplyReportsNothingWhenValuesAlreadyMatch(t *testing.T) {
	p := Project{BaseBranch: "main", Dump: true, Backup: &Backup{AppService: "backend"}}
	_, changes := ProjectUpdate{
		BaseBranch: str("main"),
		Dump:       flag(true),
		AppService: str("backend"),
	}.Apply(p)

	if len(changes) != 0 {
		t.Fatalf("nothing changed, got %+v", changes)
	}
}

// An empty value is a real edit: it is how a command or a base branch gets
// dropped again.
func TestApplyClearsAValueOnPurpose(t *testing.T) {
	p := Project{Backup: &Backup{DepsCommand: "poetry install"}}
	edited, changes := ProjectUpdate{DepsCommand: str("")}.Apply(p)

	if edited.Backup.DepsCommand != "" {
		t.Fatalf("deps command should be gone, got %q", edited.Backup.DepsCommand)
	}
	if len(changes) != 1 || changes[0].From != "poetry install" || changes[0].To != "" {
		t.Fatalf("changes = %+v", changes)
	}
}

// migrations_path decides whether a dump is reported as stale, and the flag
// path is the only way in for a project whose migrations sit outside the
// default pathspec.
func TestApplyRecordsTheMigrationsPath(t *testing.T) {
	p := Project{Backup: &Backup{AppService: "api", MigrateCommand: "rails db:migrate"}}
	edited, changes := ProjectUpdate{MigrationsPath: str("db/migrate/*")}.Apply(p)

	if edited.BackupConfig().MigrationsPath != "db/migrate/*" {
		t.Fatalf("backup = %+v", edited.Backup)
	}
	if len(changes) != 1 || changes[0].Field != "migrations_path" {
		t.Fatalf("changes = %+v", changes)
	}
}

// Two env maps of one size differing only by a key whose value is empty used
// to read as equal, so renaming such a variable was applied nowhere and
// reported as nothing.
func TestApplyReportsARenamedEmptyEnvVariable(t *testing.T) {
	p := Project{Backup: &Backup{Env: map[string]string{"DB_NAME": "{{database}}", "OLD": ""}}}
	edited, changes := ProjectUpdate{
		Env: map[string]string{"DB_NAME": "{{database}}", "NEW": ""},
	}.Apply(p)

	if _, ok := edited.Backup.Env["OLD"]; ok {
		t.Fatalf("the old variable should be gone: %v", edited.Backup.Env)
	}
	if len(changes) != 1 || changes[0].Field != "env" {
		t.Fatalf("changes = %+v", changes)
	}
}
