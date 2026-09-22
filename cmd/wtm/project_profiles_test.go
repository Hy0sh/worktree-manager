package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// parsedFlags returns the settings a command line carries, the way create and
// edit read them.
func parsedFlags(t *testing.T, args ...string) (*projectFlags, *cobra.Command) {
	t.Helper()
	f := &projectFlags{}
	cmd := &cobra.Command{Use: "edit"}
	f.bind(cmd)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return f, cmd
}

func TestProfileSetRecordsTheProfilesAndReplacesTheSet(t *testing.T) {
	f, cmd := parsedFlags(t, "--profile-set", "light=db,backend", "--profile-set", "async=db,celery_worker")
	u, err := f.update(cmd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	p, changes := u.Apply(config.Project{Profiles: map[string][]string{"old": {"db"}}})
	want := map[string][]string{"light": {"db", "backend"}, "async": {"db", "celery_worker"}}
	if !reflect.DeepEqual(p.Profiles, want) {
		t.Fatalf("profiles = %v, want %v (the set is replaced, never merged)", p.Profiles, want)
	}
	if len(changes) != 1 || changes[0].Field != "profiles" {
		t.Fatalf("changes = %v, want the profiles line alone", changes)
	}
}

// An edit that says nothing about the profiles must leave them alone, like
// every other field.
func TestProfilesSurviveAnEditThatDoesNotNameThem(t *testing.T) {
	f, cmd := parsedFlags(t, "--base", "develop")
	u, err := f.update(cmd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	p, _ := u.Apply(config.Project{Profiles: map[string][]string{"light": {"db"}}})
	if len(p.Profiles) != 1 {
		t.Fatalf("profiles = %v, an unrelated edit must not drop them", p.Profiles)
	}
}

func TestProfileSetRefusesWhatStartWouldRefuseLater(t *testing.T) {
	for _, arg := range []string{"light", "light=", "=db", "light=my Backend"} {
		f, cmd := parsedFlags(t, "--profile-set", arg)
		if _, err := f.update(cmd); err == nil {
			t.Fatalf("--profile-set %q should be refused at registration", arg)
		}
	}
}

// flagForSetting names the flag of every registry field whose flag is not its
// json name with dashes.
var flagForSetting = map[string]string{
	"base_branch":     "base",
	"deps_command":    "deps",
	"migrate_command": "migrate",
	"profiles":        "profile-set",
}

// settingsWithoutAFlag are the fields the command line deliberately leaves
// out, with the reason. Anything else added to the registry is expected to be
// settable without opening config.json: `profiles` shipped without a flag for
// two releases, which this test exists to catch.
var settingsWithoutAFlag = map[string]string{
	"port_offset":      "an edit must not renumber the stacks already running",
	"worktree_indices": "state wtm records, not a setting",
	"worktree_paths":   "state wtm records, not a setting",
}

func TestEverySettingHasAFlag(t *testing.T) {
	_, cmd := parsedFlags(t)
	seen := map[string]bool{}
	for _, section := range []any{config.Project{}, config.Backup{}} {
		for _, field := range reflect.VisibleFields(reflect.TypeOf(section)) {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" || name == "backup" { // the section itself, walked on its own
				continue
			}
			seen[name] = true
			if _, excluded := settingsWithoutAFlag[name]; excluded {
				continue
			}
			flag := flagForSetting[name]
			if flag == "" {
				flag = strings.ReplaceAll(name, "_", "-")
			}
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("%s is settable in config.json but has no --%s flag: add one, "+
					"or list it in settingsWithoutAFlag with the reason", name, flag)
			}
		}
	}
	for name := range settingsWithoutAFlag {
		if !seen[name] {
			t.Errorf("settingsWithoutAFlag names %q, which the registry no longer has", name)
		}
	}
}
