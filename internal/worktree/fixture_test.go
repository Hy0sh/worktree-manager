package worktree

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// fixture builds a fake project repo plus a fake runner whose `git worktree
// add` actually creates the destination, so the file-copy steps have somewhere
// to write.
type fixture struct {
	root         string
	backups      string
	cfgPath      string
	fake         *execx.Fake
	branchHead   bool              // whether refs/heads/<branch> already exists
	remotes      []string          // remotes the fake repository has
	pushedOn     []string          // remotes that carry the branch
	needsFetch   bool              // those remotes only reveal it once fetched
	fetched      bool              // set when the fake handled a fetch
	dockerLabels []string          // compose project labels `docker ps -a` reports
	tracked      map[string]string // files the checkout carries, written by `worktree add`
	// envTracked has git answer that .env is versioned, which stops wtm from
	// writing the port block into it. Off by default: a project gitignoring its
	// .env is the ordinary case, and it is the one where the ports are written.
	envTracked bool
	lockReason string // set to have the listing report the worktree as locked
	// foreign are the worktrees git lists outside .worktrees, branch -> path:
	// what `wtm adopt` exists for.
	foreign map[string]string
	// managed are the branches the registry holds an index for, which is what
	// makes a foreign worktree visible to the listing.
	managed map[string]bool
	// cwd is the branch CurrentWorktree answers from; empty means the command
	// was typed in the main repository. cwdDetached drops the branch from that
	// answer without moving the directory.
	cwd         string
	cwdDetached bool
}

// worktreePath is where the fixture puts a branch: a foreign path when one was
// declared, and the place wtm creates its own otherwise.
func (f *fixture) worktreePath(branch string) string {
	if p, ok := f.foreign[branch]; ok {
		return p
	}
	return filepath.Join(f.root, ".worktrees", filepath.FromSlash(branch))
}

// hasTrackingRef answers `rev-parse refs/remotes/<remote>/<branch>` for the
// fake, which is what tells wtm a branch exists on a remote.
func (f *fixture) hasTrackingRef(line string) bool {
	for _, r := range f.pushedOn {
		if strings.Contains(line, "refs/remotes/"+r+"/") {
			return !f.needsFetch || f.fetched
		}
	}
	return false
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir(), backups: t.TempDir(),
		cfgPath: filepath.Join(t.TempDir(), "config.json")}
	if err := config.WithLock(f.cfgPath, func(c *config.Config) error {
		c.Projects["myapp"] = config.Project{Dir: f.root}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(f.root, "compose.yaml"), `services:
  db:
    ports:
      - "${DB_PORT:-5432}:5432"
  backend:
    ports:
      - "${BACKEND_PORT:-8000}:8000"
`)
	mustWrite(t, filepath.Join(f.root, ".wtcrc.json"), `{"portStride": 7}`)
	f.fake = &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		line := c.String()
		switch {
		case strings.Contains(line, "rev-parse --verify"):
			if strings.Contains(line, "refs/heads/") && f.branchHead || f.hasTrackingRef(line) {
				return execx.Result{Stdout: "abc123\n"}, nil
			}
			return execx.Result{ExitCode: 1}, errors.New("unknown revision")
		case strings.Contains(line, "ls-files --error-unmatch"):
			// tracked() concludes on err == nil, so answering everything
			// successfully would make git look like it versions every file, and
			// the port block would never be written in any test.
			if f.envTracked {
				return execx.Result{Stdout: ".env\n"}, nil
			}
			return execx.Result{ExitCode: 1}, errors.New("did not match any file(s) known to git")
		case strings.HasSuffix(line, " remote"):
			return execx.Result{Stdout: strings.Join(f.remotes, "\n") + "\n"}, nil
		case strings.Contains(line, "fetch --quiet"):
			f.fetched = true
			for _, r := range f.pushedOn {
				if strings.Contains(line, "fetch --quiet "+r+" ") {
					return execx.Result{}, nil
				}
			}
			return execx.Result{ExitCode: 128}, errors.New("couldn't find remote ref")
		case strings.Contains(line, "worktree add"):
			// `worktree add -b <branch> <dest> <base>` and
			// `worktree add <dest> <branch>` both put dest second to last.
			dest := c.Args[len(c.Args)-2]
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return execx.Result{}, err
			}
			for rel, body := range f.tracked {
				p := filepath.Join(dest, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return execx.Result{}, err
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					return execx.Result{}, err
				}
			}
			return execx.Result{}, nil
		case strings.Contains(line, "worktree remove"):
			return execx.Result{}, os.RemoveAll(c.Args[len(c.Args)-1])
		case strings.Contains(line, "rev-parse --absolute-git-dir"),
			strings.Contains(line, "rev-parse --git-common-dir"):
			return execx.Result{Stdout: filepath.Join(f.root, ".git") + "\n"}, nil
		case strings.Contains(line, "worktree list --porcelain"):
			lock := ""
			if f.lockReason != "" {
				lock = "locked " + f.lockReason + "\n"
			}
			out := "worktree " + f.root + "\nbranch refs/heads/develop\n\n" +
				"worktree " + filepath.Join(f.root, ".worktrees", "feat", "x") + "\nbranch refs/heads/feat/x\n" + lock
			for branch, path := range f.foreign {
				head := "branch refs/heads/" + branch
				if f.cwdDetached && branch == f.cwd {
					head = "detached"
				}
				out += "\nworktree " + path + "\nHEAD abc123\n" + head + "\n"
			}
			return execx.Result{Stdout: out}, nil
		case strings.Contains(line, "branch -m"):
			// git moves the worktree's HEAD with the branch, so the listing
			// answers under the new name from here on.
			from, to := c.Args[len(c.Args)-2], c.Args[len(c.Args)-1]
			if path, ok := f.foreign[from]; ok {
				delete(f.foreign, from)
				f.foreign[to] = path
			}
			if f.cwd == from {
				f.cwd = to
			}
			return execx.Result{}, nil
		case strings.Contains(line, "--show-toplevel"):
			top, gitDir := f.root, filepath.Join(f.root, ".git")
			if f.cwd != "" {
				top = f.worktreePath(f.cwd)
				gitDir = filepath.Join(f.root, ".git", "worktrees", "w")
			}
			return execx.Result{Stdout: strings.Join(
				[]string{top, gitDir, filepath.Join(f.root, ".git")}, "\n") + "\n"}, nil
		case strings.Contains(line, "symbolic-ref"):
			if f.cwd == "" {
				return execx.Result{Stdout: "develop\n"}, nil
			}
			if f.cwdDetached {
				return execx.Result{ExitCode: 1}, errors.New("HEAD is not a symbolic ref")
			}
			return execx.Result{Stdout: f.cwd + "\n"}, nil
		case strings.Contains(line, "ps -a"):
			return execx.Result{Stdout: strings.Join(f.dockerLabels, "\n") + "\n"}, nil
		case strings.Contains(line, "volume ls"):
			return execx.Result{}, nil
		}
		return execx.Result{}, nil
	}}
	return f
}

func (f *fixture) opts(branch string) Options {
	return Options{
		Name:       "myapp",
		Project:    config.Project{Dir: f.root},
		Branch:     branch,
		Base:       "develop",
		BackupsDir: f.backups,
		Runner:     f.fake,
		Out:        io.Discard,
		Stack: &stack.Client{Runner: f.fake, Dir: f.root, Out: io.Discard,
			Managed: f.managed},
		Resolver: &index.Resolver{ConfigPath: f.cfgPath, Runner: f.fake,
			Name: "myapp", RepoName: filepath.Base(f.root), Out: io.Discard},
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lastCall returns the last recorded command containing want, or "".
func lastCall(f *fixture, want string) string {
	var found string
	for _, l := range f.fake.Lines() {
		if strings.Contains(l, want) {
			found = l
		}
	}
	return found
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
