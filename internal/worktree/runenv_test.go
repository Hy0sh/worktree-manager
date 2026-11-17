package worktree

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// runEnvOpts is a project that publishes db through a variable (5432) and auth
// as a literal (4434), the two shapes run_env has to resolve.
func runEnvOpts(t *testing.T, f *fixture, env map[string]string) Options {
	t.Helper()
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  db:
    ports:
      - "${DB_PORT:-5432}:5432"
  auth:
    ports:
      - "4434:4434"
`)
	o := f.opts("feat/x")
	o.Name = "my-app"
	o.Project.WorktreeIndices = map[string]int{"feat/x": 1}
	o.Project.RunEnv = env
	return o
}

func TestRunSetsRunEnvOnLiteralAndParameterisedPorts(t *testing.T) {
	f := newFixture(t)
	o := runEnvOpts(t, f, map[string]string{
		"AUTH_URL": "http://localhost:{{port:auth:4434}}",
		"DB_URL":   "postgresql://app@localhost:{{port:db:5432}}/my_app",
	})
	if err := Run(context.Background(), o, []string{"pnpm", "script"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	env := f.fake.Calls[len(f.fake.Calls)-1].Env
	for _, want := range []string{
		"AUTH_URL=http://localhost:24441",
		"DB_URL=postgresql://app@localhost:25439/my_app",
	} {
		if !slices.Contains(env, want) {
			t.Fatalf("env = %v, want %q", env, want)
		}
	}
}

// A host URL interpolated into a compose file would land inside a container.
func TestRunEnvStaysOutOfDockerComposeCalls(t *testing.T) {
	f := newFixture(t)
	o := runEnvOpts(t, f, map[string]string{"AUTH_URL": "http://localhost:{{port:auth:4434}}"})
	o.Project.Backup = &config.Backup{AppService: "auth"}
	if err := Exec(context.Background(), o, "", []string{"sh"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	last := f.fake.Calls[len(f.fake.Calls)-1]
	if !strings.HasPrefix(last.Line(), "docker compose") {
		t.Fatalf("last call = %q, want a docker compose call", last.Line())
	}
	for _, e := range last.Env {
		if strings.HasPrefix(e, "AUTH_URL=") {
			t.Fatalf("docker compose received %q", e)
		}
	}
}

func TestRunRefusesARunEnvTemplateTheBranchDoesNotPublish(t *testing.T) {
	f := newFixture(t)
	o := runEnvOpts(t, f, map[string]string{"CACHE_URL": "redis://localhost:{{port:cache:6379}}"})
	before := len(f.fake.Calls)
	err := Run(context.Background(), o, []string{"pnpm", "script"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"CACHE_URL", "cache:6379", "wtm project edit my-app --run-env"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}
	for _, c := range f.fake.Calls[before:] {
		if c.Name == "pnpm" {
			t.Fatal("the command must not run")
		}
	}
}

// With no recorded index there is no port to compute: nothing is set, and the
// warning already printed by Run says so.
func TestRunEnvIsNotSetBeforeTheStackWasStarted(t *testing.T) {
	f := newFixture(t)
	o := runEnvOpts(t, f, map[string]string{"AUTH_URL": "http://localhost:{{port:auth:4434}}"})
	o.Project.WorktreeIndices = nil
	if err := Run(context.Background(), o, []string{"pnpm", "script"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if env := f.fake.Calls[len(f.fake.Calls)-1].Env; len(env) != 0 {
		t.Fatalf("env = %v, want none", env)
	}
}

func TestRunEnvRevalidatesAHandEditedConfig(t *testing.T) {
	f := newFixture(t)
	o := runEnvOpts(t, f, map[string]string{"COMPOSE_FILE": "x"})
	if err := Run(context.Background(), o, []string{"pnpm"}); err == nil {
		t.Fatal("a reserved key should be refused")
	}
}

func TestEnvIncludesRunEnv(t *testing.T) {
	f := newFixture(t)
	o := runEnvOpts(t, f, map[string]string{"AUTH_URL": "http://localhost:{{port:auth:4434}}"})
	env, err := Env(context.Background(), o)
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	if !slices.Contains(env, "AUTH_URL=http://localhost:24441") {
		t.Fatalf("env = %v", env)
	}
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "COMPOSE_FILE=") }) {
		t.Fatalf("env = %v, the compose variables must stay", env)
	}
}
