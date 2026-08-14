package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("missing config must not be an error, got %v", err)
	}
	if len(cfg.Projects) != 0 {
		t.Fatalf("expected no projects, got %v", cfg.Projects)
	}
}

func TestLoadInvalidJSONMentionsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error should mention the config path, got %q", err.Error())
	}
}

func TestSaveThenLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg := &Config{
		DefaultBaseBranch: "develop",
		Projects: map[string]Project{
			"my-app": {Dir: "/repo/myapp", BaseBranch: "main", Dump: true},
		},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := back.Get("my-app")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Dir != "/repo/myapp" || p.BaseBranch != "main" || !p.Dump {
		t.Fatalf("roundtrip lost data: %+v", p)
	}
}

func TestBaseBranchPrecedence(t *testing.T) {
	cfg := &Config{DefaultBaseBranch: "develop"}
	if got := cfg.BaseBranchFor(Project{BaseBranch: "main"}); got != "main" {
		t.Fatalf("project base branch must win, got %q", got)
	}
	if got := cfg.BaseBranchFor(Project{}); got != "develop" {
		t.Fatalf("default base branch must apply, got %q", got)
	}
	empty := &Config{}
	if got := empty.BaseBranchFor(Project{}); got != FallbackBaseBranch {
		t.Fatalf("fallback = %q, want %q", got, FallbackBaseBranch)
	}
}

func TestGetUnknownProjectListsKnownOnes(t *testing.T) {
	cfg := &Config{Projects: map[string]Project{"alpha": {Dir: "/a"}, "beta": {Dir: "/b"}}}
	_, err := cfg.Get("gamma")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"gamma", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestResolveCurrentMatchesRepoRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Projects: map[string]Project{"here": {Dir: dir + "/"}}}
	name, p, err := cfg.ResolveCurrent(dir)
	if err != nil {
		t.Fatalf("ResolveCurrent: %v", err)
	}
	if name != "here" || filepath.Clean(p.Dir) != filepath.Clean(dir) {
		t.Fatalf("resolved %q %+v", name, p)
	}
}

func TestResolveCurrentUnknownRepoSuggestsProjectCreate(t *testing.T) {
	cfg := &Config{Projects: map[string]Project{"here": {Dir: "/elsewhere"}}}
	_, _, err := cfg.ResolveCurrent("/somewhere")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "/somewhere") || !strings.Contains(err.Error(), "project create") {
		t.Fatalf("error should mention the path and project create, got %q", err.Error())
	}
}
