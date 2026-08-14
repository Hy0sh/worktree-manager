package dockermem

import (
	"context"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"334.2MiB": 350434099,  // 334.2 * 1024²
		"1.47GiB":  1578400481, // 1.47 * 1024³
		"6GiB":     6442450944,
		"90.51MiB": 94906613,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil {
			t.Fatalf("ParseSize(%q): %v", in, err)
		}
		// float maths, so allow a byte or two of slack
		if diff := got - want; diff > 2 || diff < -2 {
			t.Fatalf("ParseSize(%q) = %d, want ~%d", in, got, want)
		}
	}
	if _, err := ParseSize("beaucoup"); err == nil {
		t.Fatal("an unparsable size should be an error")
	}
}

func fake(total string, ps string, stats string) *execx.Fake {
	return &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		switch {
		case strings.Contains(c.String(), "info"):
			return execx.Result{Stdout: total}, nil
		case strings.Contains(c.String(), "stats"):
			return execx.Result{Stdout: stats}, nil
		default:
			return execx.Result{Stdout: ps}, nil
		}
	}}
}

func TestReadSumsUsageAndCountsProjects(t *testing.T) {
	u, err := Read(context.Background(), fake(
		"12530000000\n",
		"myapp-backend-1|my-app|false\nmyapp-db-1|my-app|false\nwt-backend-1|my-app-wt-1-feat-x|false\n",
		"myapp-backend-1|334.2MiB / 6GiB\nmyapp-db-1|90.51MiB / 1GiB\nwt-backend-1|1.47GiB / 11.67GiB\n",
	))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if u.Total != 12530000000 {
		t.Fatalf("total = %d", u.Total)
	}
	if want := int64(350434099 + 1578400481 + 94906613); u.Used < want-8 || u.Used > want+8 {
		t.Fatalf("used = %d, want ~%d", u.Used, want)
	}
	if u.Projects != 2 {
		t.Fatalf("projects = %d, want 2 distinct compose projects", u.Projects)
	}
}

// A `compose run` container burning 6 GB is real memory, but it is not what a
// stack costs: counting it would triple the per-stack estimate.
func TestReadExcludesOneOffContainersFromTheEstimate(t *testing.T) {
	u, err := Read(context.Background(), fake(
		"12530000000\n",
		"myapp-backend-1|my-app|false\nmyapp-backend-run-abc|my-app|True\n",
		"myapp-backend-1|334.2MiB / 6GiB\nmyapp-backend-run-abc|6GiB / 11.67GiB\n",
	))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := int64(350434099 + 6442450944); u.Used < want-8 || u.Used > want+8 {
		t.Fatalf("total usage must still count the one-off, got %d want ~%d", u.Used, want)
	}
	if want := int64(350434099); u.StackUsed < want-8 || u.StackUsed > want+8 {
		t.Fatalf("stack usage = %d, want ~%d (one-off excluded)", u.StackUsed, want)
	}
	if got := u.PerProject(); got > 400*1024*1024 {
		t.Fatalf("per stack = %s, the migration container should not inflate it", Human(got))
	}
}

// Declared limits are meaningless here: one stack declaring 13 GB of mem_limit
// runs in about 2 GB. The estimate has to come from what is measured.
func TestTightUsesMeasuredAveragePerStack(t *testing.T) {
	const gib = int64(1) << 30
	// 11.7 GB VM, 2 stacks using 2 GB each: a third fits comfortably.
	u := Usage{Total: 11700 * 1000 * 1000, Used: 4 * gib, StackUsed: 4 * gib, Projects: 2}
	if u.PerProject() != 2*gib {
		t.Fatalf("per project = %d, want 2 GiB", u.PerProject())
	}
	if u.Tight() {
		t.Fatalf("6 GiB projected out of ~10.9 GiB should not warn")
	}
	if u.Warning() != "" {
		t.Fatalf("no warning expected, got %q", u.Warning())
	}

	// Four stacks already: a fifth would cross the threshold.
	u = Usage{Total: 11700 * 1000 * 1000, Used: 8 * gib, StackUsed: 8 * gib, Projects: 4}
	if !u.Tight() {
		t.Fatal("10 GiB projected out of ~10.9 GiB should warn")
	}
	msg := u.Warning()
	for _, want := range []string{"4 stack", "Go", "wtm stop"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning %q should mention %q", msg, want)
		}
	}
}

func TestTightIsSilentWithoutData(t *testing.T) {
	if (Usage{}).Tight() {
		t.Fatal("no measurement means no warning")
	}
	if (Usage{Total: 1000, Used: 900, StackUsed: 900, Projects: 0}).Tight() {
		t.Fatal("without a running stack there is nothing to extrapolate from")
	}
}
