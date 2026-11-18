package config

import (
	"strings"
	"testing"
)

func TestValidateRunEnv(t *testing.T) {
	ok := map[string]string{
		"AUTH_URL": "http://localhost:{{port:auth:4434}}",
		"_DB":      "postgresql://u:p@localhost:{{port:db:5432}}/x?alt={{port:db:5432}}",
		"PLAIN":    "value",
	}
	if err := ValidateRunEnv(ok); err != nil {
		t.Fatalf("should be accepted: %v", err)
	}
	for key, value := range map[string]string{
		"1BAD":                 "x",
		"A-B":                  "x",
		"":                     "x",
		"COMPOSE_PROJECT_NAME": "x",
		"COMPOSE_FILE":         "x",
		"WTM_DEPS_KEY":         "x",
		"WTM_DEPS_EXTERNAL":    "x",
		"A":                    "{{database}}",
		"B":                    "{{port:db}}",
		"C":                    "{{port:db:abc}}",
		"D":                    "{{port:DB:5432}}",
		"F":                    "{{ port:db:5432 }}",
	} {
		if err := ValidateRunEnv(map[string]string{key: value}); err == nil {
			t.Fatalf("%q=%q should be rejected", key, value)
		}
	}
	err := ValidateRunEnv(map[string]string{"A": "{{database}}"})
	if err == nil || !strings.Contains(err.Error(), "{{port:<service>:<container port>}}") {
		t.Fatalf("error %v should show the expected form", err)
	}
}

func TestExpandRunEnv(t *testing.T) {
	ports := map[string]int{"auth:4434": 24441, "db:5432": 25439}
	lookup := func(service, container string) (int, bool) {
		p, ok := ports[service+":"+container]
		return p, ok
	}
	got, err := ExpandRunEnv("http://localhost:{{port:auth:4434}}|{{port:db:5432}}", lookup)
	if err != nil || got != "http://localhost:24441|25439" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ExpandRunEnv("{{port:cache:6379}}", lookup); err == nil ||
		!strings.Contains(err.Error(), "cache:6379") {
		t.Fatalf("an unresolvable template should name service:port, got %v", err)
	}
	if got, err := ExpandRunEnv("plain", func(string, string) (int, bool) { return 0, false }); err != nil || got != "plain" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// A run_env value may hold a password: the change report lists keys only.
func TestApplyRunEnvReplacesTheSetAndReportsKeysOnly(t *testing.T) {
	p := Project{RunEnv: map[string]string{"OLD": "s3cret"}}
	edited, changes := ProjectUpdate{RunEnv: map[string]string{"NEW": "pw"}}.Apply(p)
	if len(edited.RunEnv) != 1 || edited.RunEnv["NEW"] != "pw" {
		t.Fatalf("run_env = %v", edited.RunEnv)
	}
	if len(changes) != 1 || changes[0].Field != "run_env" ||
		changes[0].From != "[OLD]" || changes[0].To != "[NEW]" {
		t.Fatalf("changes = %+v", changes)
	}
	if edited, _ = (ProjectUpdate{RunEnv: map[string]string{}}).Apply(edited); len(edited.RunEnv) != 0 {
		t.Fatalf("an empty set should clear run_env, got %v", edited.RunEnv)
	}
	if (ProjectUpdate{RunEnv: map[string]string{}}).IsEmpty() {
		t.Fatal("an update naming run_env is not empty")
	}
	if edited, _ = (ProjectUpdate{Dump: flag(true)}).Apply(p); edited.RunEnv["OLD"] != "s3cret" {
		t.Fatal("an unrelated edit must keep run_env")
	}
}
