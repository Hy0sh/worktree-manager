package main

import (
	"maps"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

func TestRunEnvReplacesTheSetAndAnEmptyOneClearsIt(t *testing.T) {
	f, cmd := parsedFlags(t, "--run-env", "A_URL=http://localhost:{{port:auth:4434}}", "--run-env", "B=1")
	u, err := f.update(cmd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	p, _ := u.Apply(config.Project{RunEnv: map[string]string{"OLD": "x"}})
	want := map[string]string{"A_URL": "http://localhost:{{port:auth:4434}}", "B": "1"}
	if !maps.Equal(p.RunEnv, want) {
		t.Fatalf("run_env = %v, want %v", p.RunEnv, want)
	}

	f, cmd = parsedFlags(t, "--run-env", "")
	u, err = f.update(cmd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p, _ = u.Apply(p); len(p.RunEnv) != 0 {
		t.Fatalf("run_env = %v, --run-env '' should clear it", p.RunEnv)
	}
}

func TestRunEnvRefusesABadKeyOrTemplate(t *testing.T) {
	for _, pair := range []string{"1BAD=x", "COMPOSE_FILE=x", "X={{database}}", "no-equals"} {
		f, cmd := parsedFlags(t, "--run-env", pair)
		if _, err := f.update(cmd); err == nil {
			t.Fatalf("--run-env %q should be refused", pair)
		}
	}
}
