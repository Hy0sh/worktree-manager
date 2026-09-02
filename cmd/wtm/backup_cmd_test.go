package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/backup"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// backupFixture registers a project whose dump exists on disk, so `backup
// remove` has something to delete.
func backupFixture(t *testing.T, in string) (*app, string) {
	t.Helper()
	root := t.TempDir()
	a := &app{
		cfg:     &config.Config{Projects: map[string]config.Project{"myapp": {Dir: t.TempDir()}}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: root,
		runner:  &execx.Fake{},
		out:     &bytes.Buffer{},
		in:      strings.NewReader(in),
	}
	dump := (&backup.Manager{Root: root}).DumpPath("myapp")
	if err := os.MkdirAll(filepath.Dir(dump), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dump, []byte("PGDMP fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	return a, dump
}

// The dump is the migration history wtm exists not to replay, and this command
// takes the current directory's project: from the wrong one it used to delete it
// without a word, where `project remove` asks before deleting that same file.
func TestBackupRemoveAsksBeforeDeletingTheDump(t *testing.T) {
	a, dump := backupFixture(t, "\n") // an empty answer is no
	cmd := newBackupCmd(a)
	cmd.SetArgs([]string{"remove", "myapp"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("declining must fail the command, not delete quietly")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("the refusal should name the way out for a script, got %v", err)
	}
	if _, statErr := os.Stat(dump); statErr != nil {
		t.Fatalf("the dump must still be there: %v", statErr)
	}
}

func TestBackupRemoveDeletesWhenAnsweredInAdvance(t *testing.T) {
	a, dump := backupFixture(t, "")
	cmd := newBackupCmd(a)
	cmd.SetArgs([]string{"remove", "myapp", "--yes"})
	cmd.SetOut(a.out.(*bytes.Buffer))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("backup remove --yes: %v", err)
	}
	if _, err := os.Stat(dump); !os.IsNotExist(err) {
		t.Fatalf("the dump should be gone, got %v", err)
	}
}
