package worktree

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// A raw .env block ("BACKEND_PORT=28007 DB_PORT=25439") tells nobody where to
// point a browser. Pair each service with the port it actually listens on.
func TestCreateListsServiceEndpointsAfterStart(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  backend:
    ports:
      - "${BACKEND_PORT:-8000}:8000"
  db:
    ports:
      - "${DB_PORT:-5432}:5432"
  legacy:
    ports:
      - "9000:9000"
`)
	// Copied into the worktree by Create, then read back as if wtc wrote it.
	mustWrite(t, filepath.Join(f.root, ".env"), "FOO=bar\n\n# --- wtc port overrides ---\nBACKEND_PORT=28007\nDB_PORT=25439\n# --- end wtc ---\n")

	var out strings.Builder
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "backend  http://localhost:28007") {
		t.Fatalf("a web service should get a clickable URL, got:\n%s", got)
	}
	if !strings.Contains(got, "db       localhost:25439") {
		t.Fatalf("a database should be listed without an http scheme, got:\n%s", got)
	}
	// A hardcoded port is rebased through the generated compose file, so it
	// belongs in the list like any other.
	if !strings.Contains(got, "legacy   localhost:29007") {
		t.Fatalf("a rebased hardcoded port should be listed too:\n%s", got)
	}
}

// A host routed through a proxy publishes no port of its own: the proxy's
// routes say where to open it, on the port the proxy got in this worktree.
func TestEndpointsListTheURLsTheProxyRoutes(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  proxy:
    image: traefik:v3
    ports:
      - "80:80"
  front:
    depends_on: [proxy]
    labels:
      - "traefik.http.routers.front.rule=Host(`+"`front.${NAME}.localhost`"+`)"
  admin:
    labels:
      - "traefik.http.routers.admin.rule=Host(`+"`admin.${NAME}.localhost`"+`)"
`)
	// compose's rendering: ${NAME} resolved, the worktree's port published.
	next := f.fake.Handler
	f.fake.Handler = func(c execx.Cmd) (execx.Result, error) {
		if strings.Contains(c.String(), "config --format json") {
			return execx.Result{Stdout: `{"services": {
				"proxy": {"image": "traefik:v3", "ports": [{"target": 80, "published": "20087"}]},
				"front": {"labels": {"traefik.http.routers.front.rule": "Host(` + "`front.shop.localhost`" + `)"}},
				"admin": {"labels": {"traefik.http.routers.admin.rule": "Host(` + "`admin.shop.localhost`" + `)"}}}}`}, nil
		}
		return next(c)
	}
	o := f.opts("feat/x")
	o.Project.Profiles = map[string][]string{"light": {"front"}}
	o.Profile = "light"
	wt := stack.Worktree{Index: 1, Branch: "feat/x", Path: f.root}
	got := strings.Join(endpoints(context.Background(), o, wt), "\n")

	if !strings.Contains(got, "front/url  http://front.shop.localhost:20087") {
		t.Errorf("the routed address should carry the proxy's worktree port:\n%s", got)
	}
	// What people open first, then the ports, each block under its title.
	if !strings.HasPrefix(got, "urls, through traefik\n  front/url") || !strings.Contains(got, "\n\nports\n  proxy") {
		t.Errorf("urls then ports, each under its title:\n%s", got)
	}
	if strings.Contains(got, "admin") {
		t.Errorf("admin was left down by the profile and must not be listed:\n%s", got)
	}
	// admin alone leaves the proxy down: nothing routes to it then.
	o.Project.Profiles["solo"] = []string{"admin"}
	o.Profile = "solo"
	if got := strings.Join(endpoints(context.Background(), o, wt), "\n"); strings.Contains(got, "/url") {
		t.Errorf("the proxy is down, no address can open:\n%s", got)
	}
	for _, c := range f.fake.Calls {
		if strings.Contains(c.Line(), "config --format json") && !c.Bounded {
			t.Errorf("the compose config call must carry a deadline: %s", c.Line())
		}
	}
}

// Most projects run no proxy, and printing their ports must not cost them a
// docker call.
func TestEndpointsAskComposeNothingWithoutAProxy(t *testing.T) {
	f := newFixture(t)
	wt := stack.Worktree{Index: 1, Branch: "feat/x", Path: f.root}
	endpoints(context.Background(), f.opts("feat/x"), wt)
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, "config --format json") {
			t.Errorf("no proxy, no call: got %q", l)
		}
	}
}

// The port block in the worktree's own .env is the whole isolation mechanism,
// and no test reached it: the fixture answered every `ls-files` successfully, so
// git looked like it versioned .env and wtm took the branch that writes nothing.
func TestCreateWritesTheAllocatedPortsIntoTheWorktreeEnv(t *testing.T) {
	f := newFixture(t)
	if err := Create(context.Background(), f.opts("feat/x")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", ".env"))
	if err != nil {
		t.Fatalf("no .env in the worktree: %v", err)
	}
	body := string(got)
	for _, want := range []string{"# --- wtc port overrides ---", "DB_PORT=", "BACKEND_PORT=", "# --- end wtc ---"} {
		if !strings.Contains(body, want) {
			t.Fatalf(".env = %q, want %q in it", body, want)
		}
	}
	// The point of the block is that these are not the compose defaults.
	for _, unwanted := range []string{"DB_PORT=5432", "BACKEND_PORT=8000"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf(".env keeps the main stack's port: %q", body)
		}
	}
}

// A tracked .env belongs to the project, and writing into it would dirty the
// worktree on every start. The generated compose file carries the ports then.
func TestCreateLeavesATrackedEnvAloneAndSaysSo(t *testing.T) {
	f := newFixture(t)
	f.envTracked = true
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=1")
	var out bytes.Buffer
	o := f.opts("feat/x")
	o.Out = &out
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "wtc port overrides") {
		t.Fatalf("a tracked .env must not be written into, got %q", got)
	}
	if !strings.Contains(out.String(), ".env is tracked by git") {
		t.Fatalf("the note should say where the ports went instead:\n%s", out.String())
	}
	// The note above promises the ports are in that file, so every one of them
	// has to be: leaving out the parametrised ones, which is what the generated
	// file used to do, isolated nothing at all here.
	gen, err := os.ReadFile(filepath.Join(f.root, ".worktrees", "feat", "x", portsOverride))
	if err != nil {
		t.Fatalf("the generated compose file should carry them: %v", err)
	}
	for _, want := range []string{"db:", "backend:", "ports: !override", `:5432"`, `:8000"`} {
		if !strings.Contains(string(gen), want) {
			t.Fatalf("%s = %q, want %q in it", portsOverride, gen, want)
		}
	}
	for _, unwanted := range []string{`"5432:5432"`, `"8000:8000"`} {
		if strings.Contains(string(gen), unwanted) {
			t.Fatalf("%s keeps the main stack's port: %q", portsOverride, gen)
		}
	}
}

// The generated file rebases what is published, but a port variable also feeds
// `environment:` (a front told where its API is), and a versioned .env holds
// none: compose reads it from wtm's environment instead, on up as on exec.
func TestATrackedEnvStillGivesComposeThePortVariables(t *testing.T) {
	f := newFixture(t)
	f.envTracked = true
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=1")
	o := f.opts("feat/x")
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var up execx.Call
	for _, c := range f.fake.Calls {
		if strings.Contains(c.Line(), "up -d") {
			up = c
		}
	}
	for _, want := range []string{"BACKEND_PORT=28007", "DB_PORT=25439"} {
		if !slices.Contains(up.Env, want) {
			t.Errorf("up env = %v, want %q", up.Env, want)
		}
	}

	o.Project.WorktreeIndices = map[string]int{"feat/x": 1}
	if err := Run(context.Background(), o, []string{"docker", "compose", "exec", "backend", "env"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if env := f.fake.Calls[len(f.fake.Calls)-1].Env; !slices.Contains(env, "BACKEND_PORT=28007") {
		t.Errorf("run env = %v, want BACKEND_PORT=28007", env)
	}
}

// `wtm env` is for the compose calls the generated override cannot reach: a
// URL built from ${HTTP_PORT} needs the variable, which no compose file sets.
func TestEnvHandsAShellWhatRunSets(t *testing.T) {
	f := newFixture(t)
	f.envTracked = true
	mustWrite(t, filepath.Join(f.root, ".env"), "ROOT=1")
	o := f.opts("feat/x")
	if err := Create(context.Background(), o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// This copy of the project predates the index Create recorded.
	if _, err := Env(context.Background(), o); err == nil || !strings.Contains(err.Error(), "wtm start feat/x") {
		t.Fatalf("err = %v, want to be told to start the stack first", err)
	}
	o.Project.WorktreeIndices = map[string]int{"feat/x": 1}
	env, err := Env(context.Background(), o)
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	for _, want := range []string{"COMPOSE_PROJECT_NAME=" + stack.ProjectName(filepath.Base(f.root), 1, "feat/x"),
		"BACKEND_PORT=28007", "DB_PORT=25439"} {
		if !slices.Contains(env, want) {
			t.Errorf("env = %v, want %q", env, want)
		}
	}
}

// shop's shape: db on 5432, db_test on 5433, no .wtcrc.json so the stride is
// 1. Index 1 puts db_test on 26434 and index 2 puts db there too.
func twoDBFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  db:
    ports:
      - "${DB_PORT:-5432}:5432"
  db_test:
    ports:
      - "${DB_TEST_PORT:-5433}:5432"
`)
	if err := os.Remove(filepath.Join(f.root, ".wtcrc.json")); err != nil {
		t.Fatal(err)
	}
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		p := c.Projects["myapp"]
		p.PortOffset = 1000
		p.WorktreeIndices = map[string]int{"feat/x": 1}
		c.Projects["myapp"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPortClashNamesTheNeighbourAndItsPort(t *testing.T) {
	f := twoDBFixture(t)
	o := f.opts("feat/y")
	o.Project.PortOffset = 1000
	why := portClash(o)(2)
	for _, want := range []string{"26434", "feat/x", "db_test", "db"} {
		if !strings.Contains(why, want) {
			t.Fatalf("reason should carry %q, got %q", want, why)
		}
	}
	if !strings.Contains(why, "portStride") {
		t.Fatalf("the remedy is portStride in .wtcrc.json, got %q", why)
	}
}

// Two recorded branches clash with index 2, one on each side. Naming whichever
// the map yielded first turned one clash into two different diagnoses.
func TestPortClashNamesTheSameNeighbourEveryRun(t *testing.T) {
	f := twoDBFixture(t)
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		p := c.Projects["myapp"]
		p.WorktreeIndices = map[string]int{"feat/x": 1, "feat/a": 3}
		c.Projects["myapp"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	o := f.opts("feat/y")
	o.Project.PortOffset = 1000
	for i := 0; i < 20; i++ {
		if why := portClash(o)(2); !strings.Contains(why, "feat/a") {
			t.Fatalf("the first neighbour in order must be the one named, got %q", why)
		}
	}
}

// Offsets step by 1000 while default ports spread wider: another project's
// 8000 at offset 0 meets this one's 3000 at offset 5000, and docker refused
// the bind with no word on why the index was handed out.
func TestPortClashSeesAnotherProjectsWorktree(t *testing.T) {
	f := twoDBFixture(t)
	other := t.TempDir()
	mustWrite(t, filepath.Join(other, "compose.yaml"), `services:
  web:
    ports:
      - "${WEB_PORT:-6435}:80"
`)
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		c.Projects["other"] = config.Project{Dir: other, WorktreeIndices: map[string]int{"feat/z": 1}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	o := f.opts("feat/y")
	o.Project.PortOffset = 1000
	why := portClash(o)(3)
	for _, want := range []string{"26436", "other", "feat/z", "port_offset"} {
		if !strings.Contains(why, want) {
			t.Fatalf("reason should carry %q, got %q", want, why)
		}
	}
}

func TestPortClashIsSilentOnAFreeIndex(t *testing.T) {
	f := twoDBFixture(t)
	o := f.opts("feat/y")
	o.Project.PortOffset = 1000
	if why := portClash(o)(3); why != "" {
		t.Fatalf("index 3 clashes with nothing, got %q", why)
	}
}

// The recorded branch itself is never its own neighbour: a start on an existing
// index must not refuse the index it already owns.
func TestPortClashIgnoresTheBranchBeingResolved(t *testing.T) {
	f := twoDBFixture(t)
	o := f.opts("feat/x")
	o.Project.PortOffset = 1000
	if why := portClash(o)(1); why != "" {
		t.Fatalf("feat/x owns index 1, got %q", why)
	}
}
