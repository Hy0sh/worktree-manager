package backup

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// The metadata only feeds the staleness heuristic. A directory git cannot read
// used to fail the refresh at its very last step, after the migrations had run
// and the dump was already on disk.
func TestRefreshKeepsTheDumpWhenTheMetadataFails(t *testing.T) {
	var out strings.Builder
	f := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "rev-parse HEAD") {
			return execx.Result{ExitCode: 128}, errors.New("fatal: not a git repository")
		}
		return okHandler(c)
	}}
	m := newManager(t, f)
	m.Out = &out

	if err := m.Refresh(context.Background(), "myapp", newProject(t)); err != nil {
		t.Fatalf("an unreadable revision must not fail the refresh: %v", err)
	}
	if _, err := os.Stat(m.DumpPath("myapp")); err != nil {
		t.Fatalf("the dump must be published: %v", err)
	}
	if !strings.Contains(out.String(), "warning:") {
		t.Fatalf("the missing metadata must be reported, output was:\n%s", out.String())
	}
}
