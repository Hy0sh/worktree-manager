package config

import (
	"path/filepath"
	"testing"
)

// The environment overrides exist so nothing ever writes into a real home
// directory, tests first of all.
func TestPathsFollowTheEnvironmentOverrides(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/wtm-config")
	t.Setenv(EnvBackupsDir, "/tmp/wtm-backups")

	if got, err := Path(); err != nil || got != "/tmp/wtm-config/config.json" {
		t.Fatalf("Path = %q, err = %v", got, err)
	}
	if got, err := BackupsDir(); err != nil || got != "/tmp/wtm-backups" {
		t.Fatalf("BackupsDir = %q, err = %v", got, err)
	}
}

// Only the config directory is set: the dumps then live under it.
func TestBackupsDirDefaultsUnderTheConfigDir(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/wtm-config")
	t.Setenv(EnvBackupsDir, "")

	got, err := BackupsDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/wtm-config", "backups"); got != want {
		t.Fatalf("BackupsDir = %q, want %q", got, want)
	}
}
