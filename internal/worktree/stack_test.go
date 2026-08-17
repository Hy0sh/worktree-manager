package worktree

import (
	"context"
	"strings"
	"testing"

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
