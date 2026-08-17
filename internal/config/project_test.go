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
