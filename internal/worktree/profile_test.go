package worktree

import (
	"context"
	"strings"
	"testing"
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
