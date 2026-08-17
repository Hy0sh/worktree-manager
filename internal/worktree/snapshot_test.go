package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

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

// The generated snapshot file is now handed to docker compose directly, as an
// extra -f, instead of being smuggled in through COMPOSE_FILE.
func TestStartPassesTheGeneratedComposeFileToDocker(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Dump = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var up string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "up -d --build") {
			up = l
		}
	}
	dest := filepath.Join(f.root, ".worktrees", "feat", "x")
	for _, want := range []string{
		"-f " + filepath.Join(dest, "compose.yaml"),
		"-f " + filepath.Join(dest, ".wtm-snapshot.yaml"),
		"--project-directory " + dest,
	} {
		if !strings.Contains(up, want) {
			t.Fatalf("up call should contain %q:\n%s", want, up)
		}
	}
}

// A worktree created before the restore existed must pick it up on start.
func TestStartRegeneratesTheSnapshotAssets(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), "services:\n  db: {}\n")
	o := f.opts("feat/x")
	o.NoStart = true
	o.Project.Dump = true
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	generated := filepath.Join(f.root, ".worktrees", "feat", "x", ".wtm-snapshot.yaml")
	if err := os.Remove(generated); err != nil {
		t.Fatal(err)
	}

	start := f.opts("feat/x")
	start.Project.Dump = true
	if err := Start(context.Background(), start); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("start should have rewritten the snapshot file: %v", err)
	}
}

// A restored database comes up migrated but empty, which is worth saying once,
// and only when the database is actually new.
func TestFreshDatabaseWarnsAboutSeeding(t *testing.T) {
	f := newFixture(t)
	var out strings.Builder
	o := f.opts("feat/x")
	o.Project.Dump = true
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(out.String(), "no seed data") || !strings.Contains(out.String(), "wtm exec feat/x") {
		t.Fatalf("a fresh database should be flagged as unseeded:\n%s", out.String())
	}
}

func TestRestartDoesNotWarnAboutSeeding(t *testing.T) {
	f := newFixture(t)
	first := f.opts("feat/x")
	first.NoStart = true
	first.Project.Dump = true
	if err := Create(context.Background(), first); err != nil {
		t.Fatalf("Create: %v", err)
	}
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "volume ls") {
			return execx.Result{Stdout: "wt_postgres_data\n"}, nil
		}
		return inner(c)
	}
	var out strings.Builder
	o := f.opts("feat/x")
	o.Project.Dump = true
	o.Out = &out
	if err := Start(context.Background(), o); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if strings.Contains(out.String(), "no seed data") {
		t.Fatalf("an existing database keeps its data, no warning expected:\n%s", out.String())
	}
}

func TestGeneratedFilesRefuseAnInjectableName(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.NoStart = true
	o.Project.Dump = true
	o.Name = `app"; rm -rf /; echo "`
	if err := Create(context.Background(), o); err == nil {
		t.Fatal("a project name that would inject shell should be refused")
	}

	o = f.opts("feat/x")
	o.NoStart = true
	o.Project.Dump = true
	o.Project.Backup = &config.Backup{DBService: "db\n  evil:\n    image: x"}
	if err := Create(context.Background(), o); err == nil {
		t.Fatal("a service name that would inject YAML should be refused")
	}
}
