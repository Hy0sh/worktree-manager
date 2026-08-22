package worktree

import (
	"context"
	"os"
	"path/filepath"
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

// A checkout can lay a symlink down where a copy lands (a branch tracking
// .env as a link, even one escaping the worktree): writing through it would
// put the developer's env values at a path the branch chose. The copy must
// replace the link with the worktree's own regular file.
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
	if string(got) != "ROOT=edited" {
		t.Fatalf(".env = %q, start overwrote a worktree edit", got)
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
