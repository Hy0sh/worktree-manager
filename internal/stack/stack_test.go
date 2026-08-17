package stack

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func newClient(t *testing.T, f *execx.Fake) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	return &Client{Runner: f, Dir: dir, Out: io.Discard}, dir
}

// Up and Down now talk to docker compose directly, with the project name that
// isolates this worktree's containers, network and volumes.
func TestUpTargetsTheWorktreeProject(t *testing.T) {
	f := &execx.Fake{}
	c, _ := newClient(t, f)
	files := []string{"/wt/compose.yaml", "/wt/.wtm-snapshot.yaml"}
	if err := c.Up(context.Background(), "myapp-wt-1-feat-x", "/wt", files); err != nil {
		t.Fatalf("Up: %v", err)
	}
	line := f.Lines()[0]
	for _, want := range []string{
		"-p myapp-wt-1-feat-x", "--project-directory /wt",
		"-f /wt/compose.yaml", "-f /wt/.wtm-snapshot.yaml", "up -d --build",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("up call %q should contain %q", line, want)
		}
	}
	if f.Calls[0].Dir != "/wt" {
		t.Fatalf("must run from the worktree, got %q", f.Calls[0].Dir)
	}
}

func TestDownKeepsVolumes(t *testing.T) {
	f := &execx.Fake{}
	c, _ := newClient(t, f)
	if err := c.Down(context.Background(), "myapp-wt-1-feat-x", "/wt"); err != nil {
		t.Fatalf("Down: %v", err)
	}
	line := f.Lines()[0]
	if !strings.HasSuffix(line, "compose -p myapp-wt-1-feat-x down") {
		t.Fatalf("down call = %q", line)
	}
	if strings.Contains(line, "-v") {
		t.Fatal("stopping must not destroy the database")
	}
}
