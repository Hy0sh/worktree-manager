package config

import (
	"strings"
	"testing"
)

// A project that says nothing about its database still gets the layout most
// compose files use, and never a zero value.
func TestBackupConfigFillsTheDefaultsIn(t *testing.T) {
	b := Project{}.BackupConfig()
	if b.DBService != DefaultDBService || b.DBUser != DefaultDBUser {
		t.Fatalf("defaults not applied: %+v", b)
	}
	if b.MigrationsPath != DefaultMigrationsPath {
		t.Fatalf("migrations path = %q", b.MigrationsPath)
	}

	own := Project{Backup: &Backup{DBService: "postgres", DBUser: "app"}}.BackupConfig()
	if own.DBService != "postgres" || own.DBUser != "app" {
		t.Fatalf("the project's own values must win: %+v", own)
	}
	if own.MigrationsPath != DefaultMigrationsPath {
		t.Fatalf("an unset field still gets its default, got %q", own.MigrationsPath)
	}
}

func TestBackupConfigDefaultsTheEngine(t *testing.T) {
	if got := (Project{}).BackupConfig().DBEngine; got != DefaultDBEngine {
		t.Fatalf("default engine = %q", got)
	}
	p := Project{Backup: &Backup{DBEngine: "mysql"}}
	if got := p.BackupConfig().DBEngine; got != "mysql" {
		t.Fatalf("engine = %q", got)
	}
}

func TestBackupConfigDefaultsTheDBPath(t *testing.T) {
	if got := (Project{}).BackupConfig().DBPath; got != DefaultDBPath {
		t.Fatalf("default db_path = %q", got)
	}
	p := Project{Backup: &Backup{DBPath: "var/data.db"}}
	if got := p.BackupConfig().DBPath; got != "var/data.db" {
		t.Fatalf("db_path = %q", got)
	}
}

func TestApplyRecordsTheDBPathChange(t *testing.T) {
	path := "var/data.db"
	p, changes := (ProjectUpdate{DBPath: &path}).Apply(Project{})
	if p.Backup == nil || p.Backup.DBPath != "var/data.db" {
		t.Fatalf("backup = %+v", p.Backup)
	}
	if len(changes) != 1 || changes[0].Field != "db_path" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestApplyRecordsTheEngineChange(t *testing.T) {
	engine := "mariadb"
	p, changes := (ProjectUpdate{DBEngine: &engine}).Apply(Project{})
	if p.Backup == nil || p.Backup.DBEngine != "mariadb" {
		t.Fatalf("backup = %+v", p.Backup)
	}
	if len(changes) != 1 || changes[0].Field != "db_engine" {
		t.Fatalf("changes = %+v", changes)
	}
}

// The error is read by someone about to run a refresh, so it has to name every
// field that is missing, not just the first one.
func TestValidateNamesEveryMissingField(t *testing.T) {
	err := Backup{}.Validate()
	if err == nil {
		t.Fatal("an empty backup configuration cannot be valid")
	}
	for _, want := range []string{"app_service", "migrate_command"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q, got %q", want, err)
		}
	}
	if err := (Backup{AppService: "backend", MigrateCommand: "make migrate"}).Validate(); err != nil {
		t.Fatalf("a complete configuration should pass: %v", err)
	}
}

func TestExpandSubstitutesEveryPlaceholder(t *testing.T) {
	got := Expand("postgresql://u@db/"+DatabasePlaceholder+"?schema="+DatabasePlaceholder, "tmp_db")
	if want := "postgresql://u@db/tmp_db?schema=tmp_db"; got != want {
		t.Fatalf("Expand = %q, want %q", got, want)
	}
}
