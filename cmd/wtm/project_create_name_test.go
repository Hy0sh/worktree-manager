package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

// create asks for the name it used to reject the command over: the registry key
// is a question like the others, and the directory already suggests an answer.
func createWithAnswers(t *testing.T, dir, answers string, args ...string) (*app, error) {
	t.Helper()
	var out bytes.Buffer
	a := &app{
		cfg:     &config.Config{Projects: map[string]config.Project{}},
		cfgPath: filepath.Join(t.TempDir(), "config.json"),
		backups: t.TempDir(),
		in:      strings.NewReader(answers),
		out:     &out,
	}
	cmd := newProjectCreateCmd(a)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Args(cmd, cmd.Flags().Args()); err != nil {
		return a, err
	}
	return a, cmd.RunE(cmd, cmd.Flags().Args())
}

func registered(t *testing.T, a *app) map[string]config.Project {
	t.Helper()
	c, err := config.Load(a.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return c.Projects
}

func TestCreateAsksForTheNameWhenItIsNotGiven(t *testing.T) {
	dir := repoWithCompose(t)
	a, err := createWithAnswers(t, dir, "webshop\n", "--dir", dir, "--base", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := registered(t, a)["webshop"]; !ok {
		t.Fatalf("the typed name should be the registry key, got %v", registered(t, a))
	}
}

// The directory is nearly always what the project is called.
func TestCreateOffersTheDirectoryAsTheName(t *testing.T) {
	dir := repoWithCompose(t)
	a, err := createWithAnswers(t, dir, "\n", "--dir", dir, "--base", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := registered(t, a)[filepath.Base(dir)]; !ok {
		t.Fatalf("an empty answer should keep the directory's name, got %v", registered(t, a))
	}
}

// A script has nobody to answer, and must be told what to pass.
func TestCreateWithoutANameAndNoInputSaysWhatToPass(t *testing.T) {
	dir := repoWithCompose(t)
	_, err := createWithAnswers(t, dir, "", "--dir", dir, "--no-input")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "`wtm project create my-app`") {
		t.Fatalf("the error should show the call, got %v", err)
	}
}

func TestAskNameRefusesWhatWouldNotDoAsAName(t *testing.T) {
	var out bytes.Buffer
	// A directory named "my project" cannot be offered: the name ends up in a
	// compose project name.
	name, err := askName(newPrompter(strings.NewReader("webshop\n"), &out), "my project")
	if err != nil {
		t.Fatalf("askName: %v", err)
	}
	if name != "webshop" {
		t.Fatalf("name = %q", name)
	}
	if strings.Contains(out.String(), "my project") {
		t.Fatalf("an unusable directory name must not be offered:\n%s", out.String())
	}

	// A typed answer gets the same treatment, and the question comes back.
	out.Reset()
	name, err = askName(newPrompter(strings.NewReader("no good\nwebshop\n"), &out), "")
	if err != nil {
		t.Fatalf("askName: %v", err)
	}
	if name != "webshop" {
		t.Fatalf("name = %q", name)
	}
	if strings.Count(out.String(), "project name:") != 2 {
		t.Fatalf("the question should come back once:\n%s", out.String())
	}
}
