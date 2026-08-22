package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Two refreshes of one project fight over the same throwaway database and
// temporary file: the second must fail fast and clearly, not queue up to
// redo the first one's work.
func TestRefreshFailsFastWhenOneIsAlreadyRunning(t *testing.T) {
	f := &execx.Fake{Handler: okHandler}
	m := newManager(t, f)
	if err := os.MkdirAll(filepath.Join(m.Root, "myapp"), 0o700); err != nil {
		t.Fatal(err)
	}
	holder := flock.New(filepath.Join(m.Root, "myapp", "refresh.lock"))
	if err := holder.Lock(); err != nil {
		t.Fatal(err)
	}
	defer holder.Unlock()

	err := m.Refresh(context.Background(), "myapp", newProject(t))
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("a concurrent refresh must be told apart, got %v", err)
	}
	if len(f.Calls) != 0 {
		t.Fatalf("nothing may run while another refresh holds the lock, got %v", f.Lines())
	}
}
