package worktree

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// A removed worktree used to leave its database volume behind forever, because
// `docker compose down` keeps volumes.
func TestRemoveDropsTheStackVolumes(t *testing.T) {
	f := newFixture(t)
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

// The volumes were dropped on removal but the images never were, and a stack
// builds one per service: 40 removed worktrees had left 153 images behind.
func TestRemoveDropsTheStackImages(t *testing.T) {
	f := newFixture(t)
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "images -q") {
			return execx.Result{Stdout: "a18a04d19ffe\n836699db4129\n"}, nil
		}
		return inner(c)
	}
	if err := Remove(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var removal string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "docker rmi") {
			removal = l
		}
	}
	if !strings.Contains(removal, "a18a04d19ffe") || !strings.Contains(removal, "836699db4129") {
		t.Fatalf("both images should be removed, got %q", removal)
	}
}

// The memory advisory is only worth anything if it reaches the user before the
// stack goes up, and nothing pinned that: a create that starts nine containers
// on a machine already full is the moment the warning exists for.
func TestCreateWarnsAboutMemoryBeforeStartingTheStack(t *testing.T) {
	f := newFixture(t)
	fullMachine(f)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	warned := strings.Index(out.String(), "warning: ")
	if warned < 0 || !strings.Contains(out.String(), "wtm stop") {
		t.Fatalf("a create onto a full machine must warn and say how to free it:\n%s", out.String())
	}
	// After the stack is up, the advice comes too late to act on.
	if started := strings.Index(out.String(), "stack started"); started < 0 || warned > started {
		t.Fatalf("the warning must come before the stack goes up:\n%s", out.String())
	}
}

// fullMachine makes the memory measurement report a machine with no room for
// another stack. The total is a gibibyte, which no machine running this suite
// has, so the daemon never looks like it shares the local kernel and the
// measure stays the sum of the containers.
func fullMachine(f *fixture) {
	inner := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		switch line := c.String(); {
		case strings.Contains(line, "info --format"):
			return execx.Result{Stdout: "1073741824\n"}, nil
		case strings.Contains(line, "ps --format"):
			return execx.Result{Stdout: "a-backend-1|proj-a|false\nb-backend-1|proj-b|false\n"}, nil
		case strings.Contains(line, "stats"):
			return execx.Result{Stdout: "a-backend-1|400MiB / 1GiB\nb-backend-1|400MiB / 1GiB\n"}, nil
		}
		return inner(c)
	}
}

// The estimate is an average over the running stacks, so it is worth a question
// and never a refusal. Answering no leaves the worktree, which is the state
// --no-start produces and is perfectly usable.
func TestCreateLetsAPersonCallOffTheStackOnMemory(t *testing.T) {
	f := newFixture(t)
	fullMachine(f)
	var out bytes.Buffer
	asked := ""
	o := f.opts("feat/x")
	o.Out = &out
	o.Confirm = func(q string) bool { asked = q; return false }
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("declining the stack is not a failed create: %v", err)
	}
	if asked == "" {
		t.Fatalf("the warning should have been followed by a question:\n%s", out.String())
	}
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "compose") && strings.Contains(l, " up") {
			t.Fatalf("no stack should have been started, got %q", l)
		}
	}
	if !strings.Contains(out.String(), "worktree ready:") {
		t.Fatalf("the worktree itself must stand:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "`wtm start feat/x`") {
		t.Fatalf("the way back must be named:\n%s", out.String())
	}
}

// Nobody wants a question about memory on a machine that has room.
func TestCreateAsksNothingWhenThereIsRoom(t *testing.T) {
	f := newFixture(t)
	o := f.opts("feat/x")
	o.Confirm = func(string) bool {
		t.Error("a create with no warning to answer must not ask anything")
		return true
	}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// Calling off the stack on memory used to lose the project's post_create and
// the create's own --exec without a word: both are played by afterCreate, which
// that path skips. `wtm start` does not replay post_create either, so the way
// back has to be named here or nowhere.
func TestCallingOffTheStackStillNamesWhatWasNotPlayed(t *testing.T) {
	f := newFixture(t)
	fullMachine(f)
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	o.Confirm = func(string) bool { return false }
	o.Project.PostCreate = "manage.py seed_data"
	o.ExecAfter = "manage.py load_fixture demo"
	o.Project.Backup = &config.Backup{AppService: "backend"}
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, want := range []string{
		"wtm exec feat/x -- sh -c 'manage.py seed_data'",
		"wtm exec feat/x -- sh -c 'manage.py load_fixture demo'",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("want %s in:\n%s", want, out.String())
		}
	}
}
