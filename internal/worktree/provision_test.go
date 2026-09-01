package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateCopiesEnvFilesAndComposeOverride(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=1")
	mustWrite(t, filepath.Join(f.root, "backend", "app.env"), "APP=1")
	mustWrite(t, filepath.Join(f.root, "a", "b", "deep.env"), "DEEP=1")
	mustWrite(t, filepath.Join(f.root, "a", "b", "c", "toodeep.env"), "TOO=1")
	mustWrite(t, filepath.Join(f.root, "node_modules", "skipped.env"), "NO=1")
	mustWrite(t, filepath.Join(f.root, "compose.override.yaml"), "services: {}")

	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	for _, rel := range []string{".env", "backend/app.env", "a/b/deep.env", "compose.override.yaml"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Fatalf("%s should have been copied: %v", rel, err)
		}
	}
	for _, rel := range []string{"a/b/c/toodeep.env", "node_modules/skipped.env"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s should not have been copied", rel)
		}
	}
}

// forceSymlink replaces symlinks and the empty directory trees Docker
// materialises at missing bind-mount sources, and nothing else: a path
// holding real content is a conflict to surface, never something to delete.
func TestForceSymlinkRefusesRealContent(t *testing.T) {
	target := t.TempDir()

	dir := t.TempDir()
	link := filepath.Join(dir, ".db-snapshot")
	mustWrite(t, filepath.Join(link, "precious.sql"), "data")
	if err := forceSymlink(target, link); err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("a directory with real files must be refused naming the path, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(link, "precious.sql")); err != nil {
		t.Fatalf("the refused content must be intact: %v", err)
	}

	file := filepath.Join(t.TempDir(), ".git-container")
	mustWrite(t, file, "tracked")
	if err := forceSymlink(target, file); err == nil {
		t.Fatal("a regular file must be refused")
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "tracked" {
		t.Fatalf("the refused file must be intact: %q, %v", got, err)
	}
}

func TestForceSymlinkToleratesFinderNoise(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, ".db-snapshot")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	// Docker materialised the directory, then Finder dropped its metadata.
	if err := os.WriteFile(filepath.Join(link, ".DS_Store"), []byte{0}, 0o644); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := forceSymlink(target, link); err != nil {
		t.Fatalf("a .DS_Store must not block the link: %v", err)
	}
	if got, _ := os.Readlink(link); got != target {
		t.Fatalf("link points at %q", got)
	}
}

func TestForceSymlinkNamesTheOffendingFile(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, ".db-snapshot")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(link, "data.sql"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := forceSymlink(t.TempDir(), link)
	if err == nil || !strings.Contains(err.Error(), "data.sql") {
		t.Fatalf("the error should name the file to move, got %v", err)
	}
}

// A checkout can lay a symlink down where a copy lands (a branch tracking .env
// as a link, even one escaping the worktree): writing through it would put the
// developer's env values at a path the branch chose, so the copy replaces it.
func TestCopyEnvFilesNeverWritesThroughADestinationSymlink(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(root, ".env"), "SECRET=1")
	outside := filepath.Join(filepath.Dir(dest), "pillaged.txt")
	if err := os.Symlink(outside, filepath.Join(dest, ".env")); err != nil {
		t.Fatal(err)
	}

	if err := copyEnvFiles(root, dest, overwriteCopies, t.Logf); err != nil {
		t.Fatalf("copyEnvFiles: %v", err)
	}
	info, err := os.Lstat(filepath.Join(dest, ".env"))
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf(".env must be the worktree's own regular file, got %v, %v", info, err)
	}
	got, err := os.ReadFile(filepath.Join(dest, ".env"))
	if err != nil || string(got) != "SECRET=1" {
		t.Fatalf(".env = %q, %v", got, err)
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written where the link pointed: %s exists", outside)
	}
}

// A dangling .env symlink in the project (.env -> .env.local before that file
// exists) is the developer's state, not a reason to fail the whole create.
func TestCopyEnvFilesSkipsADanglingSourceSymlink(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	if err := os.Symlink(".env.local", filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "backend", "app.env"), "APP=1")

	var warned bool
	logf := func(format string, args ...any) { warned = true }
	if err := copyEnvFiles(root, dest, overwriteCopies, logf); err != nil {
		t.Fatalf("a dangling symlink must be skipped, not fail the copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "backend", "app.env")); err != nil {
		t.Fatalf("the other env files must still be copied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, ".env")); !os.IsNotExist(err) {
		t.Fatal("the dangling link itself must not be copied")
	}
	if !warned {
		t.Fatal("the skip must be said, silence would hide a missing env")
	}
}

// A repository can hold a symlink pointing anywhere on disk (e.g. checked out
// from a hostile branch); following it into a copy would pull that target's
// content into the worktree. Only a target inside the project may be copied.
func TestCopyEnvFilesSkipsASymlinkLeavingTheProject(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "config.env")); err != nil {
		t.Fatal(err)
	}
	var warned []string
	logf := func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) }
	if err := copyEnvFiles(root, dest, overwriteCopies, logf); err != nil {
		t.Fatalf("a hostile link must be skipped, not fail the create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "config.env")); !os.IsNotExist(err) {
		t.Fatal("the out-of-project target was copied")
	}
	if len(warned) == 0 || !strings.Contains(warned[0], "outside the project") {
		t.Fatalf("expected a warning, got %v", warned)
	}
}

func TestCopyEnvFilesStillFollowsALinkInsideTheProject(t *testing.T) {
	// .env -> .env.local is the developer state the follow exists for.
	root, dest := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env.local"), filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if err := copyEnvFiles(root, dest, overwriteCopies, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, ".env")); string(data) != "A=1\n" {
		t.Fatalf(".env should hold the snapshot of the linked values, got %q", data)
	}
}

func TestCopyEnvFilesSkipsALinkToADirectory(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "envs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "envs"), filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if err := copyEnvFiles(root, dest, overwriteCopies, func(string, ...any) {}); err != nil {
		t.Fatalf("a directory target must be skipped, not fail the create: %v", err)
	}
}

// Every override name compose.Files detects must be copied: the start command
// passes each detected name to docker compose -f against the worktree, so a
// detected-but-not-copied override makes the stack fail on a missing file.
func TestCreateCopiesEveryComposeOverrideName(t *testing.T) {
	for _, name := range []string{
		"compose.override.yaml", "compose.override.yml",
		"docker-compose.override.yaml", "docker-compose.override.yml",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			mustWrite(t, filepath.Join(f.root, name), "services: {}")

			o := f.opts("feat/x")
			o.NoStart = true
			if err := Create(context.Background(), o); err != nil {
				t.Fatalf("Create: %v", err)
			}
			dest := filepath.Join(f.root, ".worktrees", "feat/x")
			if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
				t.Fatalf("%s should have been copied: %v", name, err)
			}
		})
	}
}

// .git-container is a macOS/VirtioFS workaround a project opts into, not
// something every repository should be littered with.
func TestCreateSkipsGitContainerByDefault(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, p := range []string{
		filepath.Join(f.root, ".worktrees", "feat", "x", ".git-container"),
		filepath.Join(f.root, ".git-container"),
	} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should not be created unless the project asks for it", p)
		}
	}
}

func TestCreateLinksGitContainerOnBothSides(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	o.Project.GitContainer = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	for _, p := range []string{filepath.Join(dest, ".git-container"), filepath.Join(f.root, ".git-container")} {
		target, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("%s should be a symlink: %v", p, err)
		}
		if target == "" {
			t.Fatalf("%s points nowhere", p)
		}
	}
}

func TestCreateLinksDbSnapshotDirectoryWhenDumpEnabled(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	o.Project.Dump = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	link := filepath.Join(f.root, ".worktrees", "feat/x", ".db-snapshot")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf(".db-snapshot should be a symlink: %v", err)
	}
	want := filepath.Join(f.backups, "myapp")
	if target != want {
		t.Fatalf("symlink target = %q, want %q (a directory, not a file)", target, want)
	}
	info, err := os.Stat(link)
	if err != nil || !info.IsDir() {
		t.Fatalf("the symlink must resolve to an existing directory: %v", err)
	}
}

func TestCreateWithoutDumpLeavesNoSnapshotLink(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	link := filepath.Join(f.root, ".worktrees", "feat/x", ".db-snapshot")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal(".db-snapshot should not exist when dump is disabled")
	}
}

// The copies and links belong to the stack, not to the checkout. A worktree
// that lost them, created before they existed or cleaned up by hand, has to
// get them back on start instead of failing inside docker.
func TestStartRestoresTheMissingProvisionedArtifacts(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=1")
	mustWrite(t, filepath.Join(f.root, "compose.override.yaml"), "services: {}")
	o := f.opts("feat/x")
	o.NoStart = true
	o.Project.Dump = true
	o.Project.GitContainer = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	dest := filepath.Join(f.root, ".worktrees", "feat", "x")
	for _, rel := range []string{".env", "compose.override.yaml", ".git-container", ".db-snapshot"} {
		if err := os.RemoveAll(filepath.Join(dest, rel)); err != nil {
			t.Fatal(err)
		}
	}

	start := f.opts("feat/x")
	start.Project.Dump = true
	start.Project.GitContainer = true
	if err := Start(context.Background(), start); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, rel := range []string{".env", "compose.override.yaml"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Fatalf("%s should have been copied again: %v", rel, err)
		}
	}
	for _, rel := range []string{".git-container", ".db-snapshot"} {
		if _, err := os.Readlink(filepath.Join(dest, rel)); err != nil {
			t.Fatalf("%s should be a symlink again: %v", rel, err)
		}
	}
}

// A .env tracked by git is already in the fresh checkout, and create still
// takes the main repository's copy as the reference, which is what carries the
// developer's local values. Only start defers to what the worktree holds.
func TestCreateOverwritesAnEnvFileTheCheckoutAlreadyCarries(t *testing.T) {
	f := newFixture(t)
	f.tracked = map[string]string{".env": "ROOT=from-the-branch"}
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=from-the-main-repo")
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ROOT=from-the-main-repo" {
		t.Fatalf(".env = %q, create should have carried the main repository's copy over", got)
	}
}

// start fills the gaps and stops there: an env file adjusted for the task at
// hand is the worktree's own state, not a stale copy to refresh.
func TestStartKeepsAnEnvFileEditedInTheWorktree(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=1")
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	dest := filepath.Join(f.root, ".worktrees", "feat", "x")
	mustWrite(t, filepath.Join(dest, ".env"), "ROOT=edited")

	if err := Start(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	// The edit survives, and the port block start writes lands beside it rather
	// than over it: that is what the marked section is for.
	if !strings.HasPrefix(string(got), "ROOT=edited") {
		t.Fatalf(".env = %q, start overwrote a worktree edit", got)
	}
	if !strings.Contains(string(got), "BACKEND_PORT=") {
		t.Fatalf(".env = %q, start should have written the ports it allocated", got)
	}
}

// Docker materialises a directory at the source of a bind-mount that does not
// exist, so what start has to replace by the link can be a non-empty directory.
func TestStartReplacesTheDirectoryDockerLeftInsteadOfTheSnapshotLink(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	o.Project.Dump = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	link := filepath.Join(f.root, ".worktrees", "feat", "x", ".db-snapshot")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(link, "restore-snapshot.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := f.opts("feat/x")
	start.Project.Dump = true
	if err := Start(context.Background(), start); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Readlink(link); err != nil {
		t.Fatalf(".db-snapshot should be a symlink again: %v", err)
	}
}

func TestCopyFileKeepModeReplacesASymlink(t *testing.T) {
	// A worktree provisioned by wtm <= 0.4.1 can hold .env as a symlink; the
	// keep mode must not preserve it, only a regular file is worth keeping.
	root, dest := t.TempDir(), t.TempDir()
	src := filepath.Join(root, ".env")
	if err := os.WriteFile(src, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dest, ".env")
	if err := os.Symlink(victim, dst); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dest, dst, keepWorktreeCopies); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "precious" {
		t.Fatalf("victim was written through: %q", data)
	}
	if info, _ := os.Lstat(dst); info.Mode()&os.ModeSymlink != 0 {
		t.Fatal(".env should now be a regular copy")
	}
}

func TestCopyEnvFilesRefusesAnIntermediateSymlinkEscape(t *testing.T) {
	root, dest, outside := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backend", "app.env"), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "backend")); err != nil {
		t.Fatal(err)
	}
	err := copyEnvFiles(root, dest, overwriteCopies, func(string, ...any) {})
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "app.env")); !os.IsNotExist(statErr) {
		t.Fatal("env values landed outside the worktree")
	}
}

func TestWriteSnapshotOverrideReplacesASymlink(t *testing.T) {
	dest := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dest, snapshotOverride)); err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshotOverride(dest, "db"); err != nil {
		t.Fatalf("writeSnapshotOverride: %v", err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "precious" {
		t.Fatalf("victim overwritten: %q", data)
	}
}
