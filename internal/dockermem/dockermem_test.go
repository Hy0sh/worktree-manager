package dockermem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	if _, err := ParseSize("lots"); err == nil {
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
	// No meminfo: this one is about summing containers, and the suite must not
	// depend on the memory of the machine it runs on.
	u, err := read(context.Background(), fake(
		"12530000000\n",
		"myapp-backend-1|my-app|false\nmyapp-db-1|my-app|false\nwt-backend-1|my-app-wt-1-feat-x|false\n",
		"myapp-backend-1|334.2MiB / 6GiB\nmyapp-db-1|90.51MiB / 1GiB\nwt-backend-1|1.47GiB / 11.67GiB\n",
	), "")
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
	u, err := read(context.Background(), fake(
		"12530000000\n",
		"myapp-backend-1|my-app|false\nmyapp-backend-run-abc|my-app|True\n",
		"myapp-backend-1|334.2MiB / 6GiB\nmyapp-backend-run-abc|6GiB / 11.67GiB\n",
	), "")
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
	for _, want := range []string{"4 stack", "GB", "wtm stop"} {
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

// meminfo writes the fields of a Linux kernel's /proc/meminfo that matter,
// surrounded by a few it must skip.
func meminfo(t *testing.T, totalKB, availableKB int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meminfo")
	body := fmt.Sprintf("MemTotal:       %d kB\nMemFree:         1234567 kB\n"+
		"MemAvailable:   %d kB\nBuffers:          123456 kB\nSwapTotal:             0 kB\n",
		totalKB, availableKB)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// On a native Linux docker the containers share the machine with the user's
// session. Counting the containers alone left the warning blind to a desktop
// holding 6 GB, and a machine froze on its third worktree, unwarned.
func TestReadCountsTheWholeMachineWhenDockerSharesIt(t *testing.T) {
	const totalKB, availableKB = 16 * 1024 * 1024, 2 * 1024 * 1024
	u, err := read(context.Background(), fake(
		// The daemon reports the same total as the local kernel: one machine.
		fmt.Sprintf("%d\n", int64(totalKB)*1024),
		"myapp-backend-1|my-app|false\nwt-backend-1|my-app-wt-1-feat-x|false\n",
		"myapp-backend-1|1GiB / 16GiB\nwt-backend-1|1GiB / 16GiB\n",
	), meminfo(t, totalKB, availableKB))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !u.Shared {
		t.Fatal("a daemon reporting the machine's own total shares its memory")
	}
	if want := int64(14) << 30; u.Used != want {
		t.Fatalf("used = %s, want %s: total minus what is available, not the containers",
			Human(u.Used), Human(want))
	}
	// The per-stack estimate still comes from the containers alone.
	if want := int64(1) << 30; u.PerProject() != want {
		t.Fatalf("per stack = %s, want %s", Human(u.PerProject()), Human(want))
	}
	if !u.Tight() {
		t.Fatal("15 GB projected out of 16 should warn")
	}
	msg := u.Warning()
	for _, want := range []string{"this machine uses", "2 stack(s) accounting for",
		"free memory elsewhere"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning %q should mention %q", msg, want)
		}
	}
	// Docker Desktop has no RAM setting on a machine that never ran one.
	if strings.Contains(msg, "Docker Desktop") {
		t.Fatalf("warning %q should not send a Linux user to Docker Desktop", msg)
	}
}

// Docker Desktop, on macOS or through WSL2, reports the budget of its own VM.
// The machine's memory says nothing about the pressure inside it, so the sum of
// the containers stays the measure. The same holds for a remote daemon.
func TestReadIgnoresTheMachineWhenTheDaemonHasItsOwnBudget(t *testing.T) {
	const totalKB, availableKB = 32 * 1024 * 1024, 20 * 1024 * 1024
	u, err := read(context.Background(), fake(
		"12530000000\n", // a 11.7 GB VM on a 32 GB machine
		"myapp-backend-1|my-app|false\n",
		"myapp-backend-1|1GiB / 11.67GiB\n",
	), meminfo(t, totalKB, availableKB))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if u.Shared {
		t.Fatal("a daemon with a total of its own does not share the machine's memory")
	}
	if want := int64(1) << 30; u.Used != want {
		t.Fatalf("used = %s, want the containers' %s", Human(u.Used), Human(want))
	}
}

// A meminfo that is missing, truncated or nonsense must leave the measure the
// way it has always been rather than fail a start.
func TestHostMemoryGivesUpQuietly(t *testing.T) {
	if _, _, ok := hostMemory(""); ok {
		t.Fatal("no path means no answer")
	}
	if _, _, ok := hostMemory(filepath.Join(t.TempDir(), "absent")); ok {
		t.Fatal("a missing meminfo means no answer")
	}
	// MemTotal alone: a kernel too old for MemAvailable answers nothing here.
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte("MemTotal:       16000000 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := hostMemory(path); ok {
		t.Fatal("without MemAvailable there is nothing to subtract")
	}
}

// realMemInfo is the head of a Linux kernel's own /proc/meminfo, taken verbatim:
// the padding, the two-word "kB" suffix and the fields to skip are the format
// this parser has to survive, and a fixture written by hand proves none of it.
const realMemInfo = `MemTotal:       12235216 kB
MemFree:         1778172 kB
MemAvailable:    7596904 kB
Buffers:         3045600 kB
Cached:           828152 kB
SwapCached:        13868 kB
Active:          3499664 kB
Inactive:        4348736 kB
Active(anon):    2525608 kB
Inactive(anon):  1549220 kB
Active(file):     974056 kB
Inactive(file):  2799516 kB
`

func TestHostMemoryReadsARealKernelsMemInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte(realMemInfo), 0o644); err != nil {
		t.Fatal(err)
	}
	used, total, ok := hostMemory(path)
	if !ok {
		t.Fatal("a real meminfo must be readable")
	}
	if want := int64(12235216) * 1024; total != want {
		t.Fatalf("total = %d, want %d", total, want)
	}
	if want := int64(12235216-7596904) * 1024; used != want {
		t.Fatalf("used = %d, want %d (total minus available)", used, want)
	}
}
