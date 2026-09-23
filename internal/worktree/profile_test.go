package worktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// profiles is the fixture's project with one named subset of its compose.
func profiles() map[string][]string {
	return map[string][]string{"light": {"db", "backend"}}
}

func upLine(lines []string) string {
	for _, l := range lines {
		if strings.Contains(l, "up -d") {
			return l
		}
	}
	return ""
}

// The whole point: the stack comes up with the services the profile names and
// not the rest of the compose file.
func TestProfileNarrowsWhatStarts(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Profiles = profiles()
	o.Profile = "light"
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if up := upLine(f.fake.Lines()); !strings.HasSuffix(up, "up -d --build db backend") {
		t.Fatalf("up = %q", up)
	}
}

// Naming none is what every worktree did before profiles existed, and has to
// keep meaning the whole stack.
func TestNoProfileStartsEverything(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Profiles = profiles()
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if up := upLine(f.fake.Lines()); !strings.HasSuffix(up, "up -d --build") {
		t.Fatalf("up = %q", up)
	}
}

// The addresses printed after a start are the ones to open: a service the
// profile left down has none, one depends_on brought up does.
func TestEndpointsListOnlyWhatTheProfileStarted(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  db:
    ports:
      - "${DB_PORT:-5432}:5432"
  mail:
    ports:
      - "${MAIL_PORT:-8025}:8025"
  admin:
    ports:
      - "${ADMIN_PORT:-5050}:80"
  backend:
    depends_on: [db, mail]
    ports:
      - "${BACKEND_PORT:-8000}:8000"
`)
	o := f.opts("feat/x")
	o.Project.Profiles = map[string][]string{"light": {"backend"}}
	o.Profile = "light"
	got := strings.Join(endpoints(o, stack.Worktree{Index: 1, Branch: "feat/x"}), "\n")
	for _, want := range []string{"db ", "mail ", "backend "} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "admin") {
		t.Errorf("admin was left down and must not be listed:\n%s", got)
	}

	o.Profile = ""
	if got := strings.Join(endpoints(o, stack.Worktree{Index: 1, Branch: "feat/x"}), "\n"); !strings.Contains(got, "admin") {
		t.Errorf("without a profile every service is listed:\n%s", got)
	}
}

// A typo would otherwise start the whole stack, and nothing in the output
// would say the profile was never applied.
func TestAnUnknownProfileFailsBeforeTheStackComesUp(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Project.Profiles = profiles()
	o.Profile = "ligth"
	err := Create(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "ligth") {
		t.Fatalf("err = %v", err)
	}
	if up := upLine(f.fake.Lines()); up != "" {
		t.Fatalf("nothing should have been started, got %q", up)
	}
}
