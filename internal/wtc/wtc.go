// Package wtc drives worktree-compose, the tool that owns port allocation and
// docker compose for worktrees. wtm never reimplements either.
package wtc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// Port override markers written by wtc into a worktree .env.
const (
	blockStart = "# --- wtc port overrides ---"
	blockEnd   = "# --- end wtc ---"
)

// Worktree is one linked worktree, with the index wtc addresses it by.
type Worktree struct {
	Index  int
	Path   string
	Branch string
}

// Client invokes wtc for one project.
type Client struct {
	Runner execx.Runner
	Dir    string // project repository root
	Out    io.Writer
	Bin    string // explicit path from the config, optional
	// Env is passed to wtc, which spreads its own environment into the
	// docker compose calls it makes. That is how an extra compose file is
	// injected without wtc knowing about it.
	Env []string
}

// ProjectName is the compose project wtc gives a worktree. Mirrors its
// composeProjectName/sanitize pair, which is the only way to address that
// stack's containers and volumes from the outside.
func ProjectName(repoName string, index int, branch string) string {
	return sanitize(fmt.Sprintf("%s-wt-%d-%s", repoName, index, branch))
}

func sanitize(input string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(input) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	collapsed := regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-")
	return strings.Trim(collapsed, "-")
}

// localBin is where a devDependency install puts the binary.
func (c *Client) localBin() string {
	return filepath.Join(c.Dir, "node_modules", ".bin", "wtc")
}

// Origin says which of the three installation routes provided the binary.
type Origin string

const (
	OriginConfigured Origin = "wtc_bin"
	OriginProject    Origin = "project"
	OriginGlobal     Origin = "global"
)

// Resolution is where wtc was found, and which version it actually is.
type Resolution struct {
	Path    string
	Origin  Origin
	Version string
}

// Locate finds wtc: an explicitly configured path first, then the project's own
// devDependency (which pins a version for the whole team), then a global
// install. That last route matters because requiring a devDependency forces a
// package.json onto projects that have no other reason for one — a PHP or
// Python repository should not carry a Node dependency for a personal tool.
func (c *Client) Locate() (Resolution, error) {
	if c.Bin != "" {
		if _, err := os.Stat(c.Bin); err != nil {
			return Resolution{}, fmt.Errorf("wtc_bin points to %s, not found: %w", c.Bin, err)
		}
		return resolution(c.Bin, OriginConfigured), nil
	}
	if local := c.localBin(); fileExists(local) {
		return resolution(local, OriginProject), nil
	}
	if path, err := exec.LookPath("wtc"); err == nil {
		return resolution(path, OriginGlobal), nil
	}
	return Resolution{}, fmt.Errorf("wtc not found, install it globally (`npm install -g worktree-compose`), "+
		"as a project devDependency (%s), or set `wtc_bin` in the config; otherwise use --no-start",
		filepath.Join(c.Dir, "node_modules", ".bin"))
}

// Resolve returns just the path to run.
func (c *Client) Resolve() (string, error) {
	r, err := c.Locate()
	return r.Path, err
}

func resolution(path string, origin Origin) Resolution {
	return Resolution{Path: path, Origin: origin, Version: packageVersion(path)}
}

// packageVersion reads the version from the package the binary belongs to.
// `wtc --version` is not usable: it reports a hardcoded 0.1.0 even on 0.2.0.
func packageVersion(bin string) string {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		real = bin
	}
	dir := filepath.Dir(real)
	for range 4 {
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err == nil {
			var pkg struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Version != "" {
				return pkg.Version
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// EnsureAvailable checks the binary before any command that needs it.
func (c *Client) EnsureAvailable() error {
	_, err := c.Resolve()
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Worktrees lists the worktrees the way wtc indexes them: `git worktree list
// --porcelain` order, main repository excluded, 1-based. Reproducing the rule
// here is more robust than parsing the coloured table `wtc list` prints.
func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error) {
	res, err := c.Runner.Run(ctx, execx.Cmd{
		Name: "git",
		Args: []string{"-C", c.Dir, "worktree", "list", "--porcelain"},
	})
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	var (
		out   []Worktree
		first = true
		path  string
		br    string
	)
	flush := func() {
		if path == "" {
			return
		}
		if first {
			first = false // the first block is the main repository
		} else {
			out = append(out, Worktree{Index: len(out) + 1, Path: path, Branch: br})
		}
		path, br = "", ""
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			br = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return out, nil
}

// FindByBranch resolves the index wtc start/stop expect.
func (c *Client) FindByBranch(ctx context.Context, branch string) (Worktree, error) {
	wts, err := c.Worktrees(ctx)
	if err != nil {
		return Worktree{}, err
	}
	for _, wt := range wts {
		if wt.Branch == branch {
			return wt, nil
		}
	}
	known := make([]string, 0, len(wts))
	for _, wt := range wts {
		known = append(known, fmt.Sprintf("%d:%s", wt.Index, wt.Branch))
	}
	list := "no linked worktree"
	if len(known) > 0 {
		list = "known worktrees: " + strings.Join(known, ", ")
	}
	return Worktree{}, fmt.Errorf("no worktree for branch %q (%s)", branch, list)
}

func (c *Client) run(ctx context.Context, args ...string) error {
	bin, err := c.Resolve()
	if err != nil {
		return err
	}
	_, err = c.Runner.Run(ctx, execx.Cmd{
		Name: bin,
		Args: args,
		Dir:  c.Dir,
		Env:  c.Env,
		Live: true,
	})
	return err
}

// Start brings the worktree stack up (sync, port injection, docker compose up).
func (c *Client) Start(ctx context.Context, index int) error {
	return c.run(ctx, "start", fmt.Sprint(index))
}

// Stop takes the worktree stack down, volumes preserved.
func (c *Client) Stop(ctx context.Context, index int) error {
	return c.run(ctx, "stop", fmt.Sprint(index))
}

// ReadPortValues returns the allocated ports keyed by environment variable.
func ReadPortValues(worktreeDir string) (map[string]string, error) {
	lines, err := ReadPorts(worktreeDir)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(lines))
	for _, line := range lines {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values, nil
}

// ReadPorts returns the port assignments wtc wrote into the worktree .env.
func ReadPorts(worktreeDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(worktreeDir, ".env"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var (
		ports  []string
		inside bool
	)
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == blockStart:
			inside = true
		case trimmed == blockEnd:
			return ports, nil
		case inside && trimmed != "":
			ports = append(ports, trimmed)
		}
	}
	return ports, nil
}
