package worktree

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/wtc"
)

// fixture builds a fake project repo plus a fake runner whose `git worktree
// add` actually creates the destination, so the file-copy steps have somewhere
// to write.
type fixture struct {
	root       string
	backups    string
	fake       *execx.Fake
	branchHead bool // whether refs/heads/<branch> already exists
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir(), backups: t.TempDir()}
	f.fake = &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		switch {
		case strings.Contains(line, "rev-parse --verify"):
			if f.branchHead {
				return execx.Result{Stdout: "abc123\n"}, nil
			}
			return execx.Result{ExitCode: 1}, errors.New("unknown revision")
		case strings.Contains(line, "worktree add"):
			// `worktree add -b <branch> <dest> <base>` and
			// `worktree add <dest> <branch>` both put dest second to last.
			dest := c.Args[len(c.Args)-2]
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return execx.Result{}, err
			}
			return execx.Result{}, nil
		case strings.Contains(line, "worktree remove"):
			return execx.Result{}, os.RemoveAll(c.Args[len(c.Args)-1])
		case strings.Contains(line, "rev-parse --absolute-git-dir"):
			return execx.Result{Stdout: filepath.Join(f.root, ".git") + "\n"}, nil
		case strings.Contains(line, "worktree list --porcelain"):
			return execx.Result{Stdout: "worktree " + f.root + "\nbranch refs/heads/develop\n\n" +
				"worktree " + filepath.Join(f.root, ".worktrees", "feat", "x") + "\nbranch refs/heads/feat/x\n"}, nil
		}
		return execx.Result{}, nil
	}}
	return f
}

func (f *fixture) opts(branch string) Options {
	return Options{
		Name:       "myapp",
		Project:    config.Project{Dir: f.root},
		Branch:     branch,
		Base:       "develop",
		BackupsDir: f.backups,
		Runner:     f.fake,
		Out:        io.Discard,
		Wtc:        &wtc.Client{Runner: f.fake, Dir: f.root, Out: io.Discard},
	}
}

func installWtc(t *testing.T, root string) {
	t.Helper()
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "wtc"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateNewBranchRunsWorktreeAddWithBase(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var addLine string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree add") {
			addLine = l
		}
	}
	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	if !strings.Contains(addLine, "-b feat/x "+dest+" develop") {
		t.Fatalf("worktree add line = %q", addLine)
	}
}

func TestCreateExistingBranchIgnoresBase(t *testing.T) {
	f := newFixture(t)
	f.branchHead = true
	var out strings.Builder
	o := f.opts("feat/x")
	o.NoStart = true
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var addLine string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree add") {
			addLine = l
		}
	}
	if strings.Contains(addLine, "-b") || strings.Contains(addLine, "develop") {
		t.Fatalf("existing branch must be reused as-is, got %q", addLine)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Fatalf("an info message about the existing branch was expected, got %q", out.String())
	}
}

func TestCreateAbortsWhenDestinationExists(t *testing.T) {
	f := newFixture(t)
	dest := filepath.Join(f.root, ".worktrees", "feat/x")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	o := f.opts("feat/x")
	o.NoStart = true
	err := Create(context.Background(), o)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), dest) {
		t.Fatalf("error should mention the destination, got %q", err.Error())
	}
	if len(f.fake.Calls) != 0 {
		t.Fatalf("no command should run when the destination exists, got %v", f.fake.Lines())
	}
}

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

func TestCreateStartsStackUnlessNoStart(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	if err := Create(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var started bool
	for _, l := range f.fake.Lines() {
		if strings.HasSuffix(l, "wtc start 1") {
			started = true
		}
	}
	if !started {
		t.Fatalf("wtc start should have run, calls = %v", f.fake.Lines())
	}
}

// The raw wtc .env block ("BACKEND_PORT=28007 DB_PORT=25439") tells nobody
// where to point a browser. Pair each service with its allocated port instead.
func TestCreateListsServiceEndpointsAfterStart(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  backend:
    ports:
      - "${BACKEND_PORT:-8000}:8000"
  db:
    ports:
      - "${DB_PORT:-5432}:5432"
  legacy:
    ports:
      - "9000:9000"
`)
	// Copied into the worktree by Create, then read back as if wtc wrote it.
	mustWrite(t, filepath.Join(f.root, ".env"), "FOO=bar\n\n# --- wtc port overrides ---\nBACKEND_PORT=28007\nDB_PORT=25439\n# --- end wtc ---\n")

	var out strings.Builder
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "backend  http://localhost:28007") {
		t.Fatalf("a web service should get a clickable URL, got:\n%s", got)
	}
	if !strings.Contains(got, "db       localhost:25439") {
		t.Fatalf("a database should be listed without an http scheme, got:\n%s", got)
	}
	if strings.Contains(got, "legacy") {
		t.Fatalf("a hardcoded port is not isolated, listing it would mislead:\n%s", got)
	}
}

func TestCreateNoStartSkipsWtcEntirely(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "wtc") {
			t.Fatalf("--no-start must not invoke wtc, got %q", l)
		}
	}
}

func TestStopUsesResolvedIndex(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	if err := Stop(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	last := f.fake.Lines()[len(f.fake.Lines())-1]
	if !strings.HasSuffix(last, "wtc stop 1") {
		t.Fatalf("last call = %q", last)
	}
}

func TestRemoveStopsThenRemovesWorktree(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	lines := f.fake.Lines()
	var stopAt, removeAt = -1, -1
	for i, l := range lines {
		if strings.HasSuffix(l, "wtc stop 1") {
			stopAt = i
		}
		if strings.Contains(l, "worktree remove") {
			removeAt = i
		}
	}
	if stopAt == -1 || removeAt == -1 || stopAt > removeAt {
		t.Fatalf("expected stop before remove, got %v", lines)
	}
}

// A worktree always holds untracked files the tool itself created (.env copies,
// .git-container, .db-snapshot), which plain `git worktree remove` refuses to
// delete. Only tracked changes are worth protecting.
func TestRemoveForcesWhenOnlyUntrackedArtifactsRemain(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var removeLine string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree remove") {
			removeLine = l
		}
	}
	if !strings.Contains(removeLine, "--force") {
		t.Fatalf("removal must force past the tool's own untracked files, got %q", removeLine)
	}
}

func TestRemoveRefusesWhenTrackedFilesAreModified(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "status --porcelain") {
			return execx.Result{Stdout: " M backend/models.py\n"}, nil
		}
		return inner(c)
	}
	err := Remove(context.Background(), f.opts("feat/x"))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "backend/models.py") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error should list the change and mention --force, got %q", err.Error())
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree remove") {
			t.Fatal("nothing should be removed when tracked files are modified")
		}
	}
}

func TestRemoveWithForceIgnoresTrackedChanges(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "status --porcelain") {
			return execx.Result{Stdout: " M backend/models.py\n"}, nil
		}
		return inner(c)
	}
	o := f.opts("feat/x")
	o.Force = true
	if err := Remove(context.Background(), o); err != nil {
		t.Fatalf("Remove --force: %v", err)
	}
	var removed bool
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "worktree remove") && strings.Contains(l, "--force") {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("--force must go through, calls = %v", f.fake.Lines())
	}
}

// A branch like feat/x nests the worktree under .worktrees/feat; git leaves
// that directory behind empty once the worktree is gone.
func TestRemovePrunesEmptyParentDirectories(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	o := f.opts("feat/x")
	o.NoStart = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees", "feat")); !os.IsNotExist(err) {
		t.Fatal(".worktrees/feat should not be left behind empty")
	}
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees")); err != nil {
		t.Fatal(".worktrees itself must survive")
	}
}

func TestRemovePropagatesGitFailure(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "worktree remove") {
			return execx.Result{}, errors.New("contains modified or untracked files")
		}
		return inner(c)
	}
	err := Remove(context.Background(), f.opts("feat/x"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "modified or untracked") {
		t.Fatalf("git error should be propagated, got %q", err.Error())
	}
}

// Relying on the project's compose to mount the dump made the behaviour depend
// on the branch the worktree was cut from, since that file is versioned. wtm
// carries the restore itself so any project gets it, on any branch.
func TestCreateShipsItsOwnRestoreMechanism(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	o.Project.Dump = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}

	script := filepath.Join(f.backups, "myapp", "restore-snapshot.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("the restore script should sit next to the dump: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("the restore script must be executable, got %v", info.Mode())
	}
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "pg_restore") || !strings.Contains(string(body), "myapp.dump") {
		t.Fatalf("restore script does not target the project dump:\n%s", body)
	}

	generated, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", ".wtm-snapshot.yaml"))
	if err != nil {
		t.Fatalf("the generated compose file is missing: %v", err)
	}
	for _, want := range []string{"db:", "/db-snapshot:ro", "docker-entrypoint-initdb.d"} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("generated compose file should contain %q:\n%s", want, generated)
		}
	}
}

func TestStartPassesTheGeneratedComposeFileToDocker(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), "services:\n  db: {}\n")
	o := f.opts("feat/x")
	o.Project.Dump = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var env []string
	for _, c := range f.fake.Calls {
		if strings.HasSuffix(c.Name, "wtc") {
			env = c.Env
		}
	}
	var found string
	for _, e := range env {
		if strings.HasPrefix(e, "COMPOSE_FILE=") {
			found = e
		}
	}
	if found == "" {
		t.Fatalf("wtc must receive COMPOSE_FILE so docker picks up the snapshot file, env = %v", env)
	}
	if !strings.Contains(found, ".wtm-snapshot.yaml") || !strings.Contains(found, "compose.yaml") {
		t.Fatalf("COMPOSE_FILE should list the project files and ours, got %q", found)
	}
}

// A removed worktree used to leave its database volume behind forever, because
// `docker compose down` keeps volumes.
func TestRemoveDropsTheStackVolumes(t *testing.T) {
	f := newFixture(t)
	installWtc(t, f.root)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "volume ls") {
			return execx.Result{Stdout: "wt_postgres_data\nwt_rustfs_data\n"}, nil
		}
		return inner(c)
	}
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var removal string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "volume rm") {
			removal = l
		}
	}
	if !strings.Contains(removal, "wt_postgres_data") || !strings.Contains(removal, "wt_rustfs_data") {
		t.Fatalf("both volumes should be removed, got %q", removal)
	}
}

// Running a command in a worktree stack otherwise means knowing the compose
// project name wtc derives, which is internal knowledge.
func TestExecTargetsTheWorktreeStack(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Exec(context.Background(), o, "", []string{"python", "manage.py", "seed_data"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	last := f.fake.Lines()[len(f.fake.Lines())-1]
	want := "docker compose -p " + wtc.ProjectName(filepath.Base(f.root), 1, "feat/x") +
		" exec backend python manage.py seed_data"
	if last != want {
		t.Fatalf("exec call =\n  %q\nwant\n  %q", last, want)
	}
	if dir := f.fake.Calls[len(f.fake.Calls)-1].Dir; dir != filepath.Join(f.root, ".worktrees", "feat", "x") {
		t.Fatalf("must run from the worktree, got %q", dir)
	}
}

func TestExecServiceFlagOverridesTheConfiguredOne(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Exec(context.Background(), o, "frontend", []string{"sh"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if last := f.fake.Lines()[len(f.fake.Lines())-1]; !strings.HasSuffix(last, "exec frontend sh") {
		t.Fatalf("--service should win, got %q", last)
	}
}

func TestExecWithoutAnyKnownServiceIsActionable(t *testing.T) {
	f := newFixture(t)
	err := Exec(context.Background(), f.opts("feat/x"), "", []string{"sh"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"--service", "app_service"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}
}
