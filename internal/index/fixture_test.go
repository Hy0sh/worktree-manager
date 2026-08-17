package index

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// newResolver seeds a registry holding one project and returns a resolver on
// it. dockerLabels drives what the fake docker reports: nil means docker is
// unreachable, an empty slice means reachable but empty.
func newResolver(t *testing.T, indices map[string]int, dockerLabels []string) (*Resolver, string, *execx.Fake) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WithLock(path, func(c *config.Config) error {
		c.Projects["myapp"] = config.Project{Dir: "/repo/my-app", WorktreeIndices: indices}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &execx.Fake{Handler: func(c execx.Cmd) (execx.Result, error) {
		if dockerLabels == nil {
			return execx.Result{ExitCode: 1}, errors.New("docker is down")
		}
		line := c.String()
		switch {
		case strings.Contains(line, "ps -a"):
			return execx.Result{Stdout: strings.Join(dockerLabels, "\n") + "\n"}, nil
		case strings.Contains(line, "volume ls"):
			// Volumes carry the same projects as k=v label lists.
			out := make([]string, 0, len(dockerLabels))
			for _, l := range dockerLabels {
				out = append(out, "com.docker.compose.version=5.1.0,com.docker.compose.project="+l)
			}
			return execx.Result{Stdout: strings.Join(out, "\n") + "\n"}, nil
		}
		return execx.Result{}, nil
	}}
	return &Resolver{ConfigPath: path, Runner: fake, Name: "myapp", RepoName: "my-app", Out: io.Discard}, path, fake
}

func recorded(t *testing.T, path, branch string) int {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Projects["myapp"].WorktreeIndices[branch]
}

// bareResolver seeds an empty registered project and returns a resolver
// wired to a custom fake, for tests that need ps -a and volume ls to answer
// independently (newResolver's fake always mirrors one into the other).
func bareResolver(t *testing.T, handler func(execx.Cmd) (execx.Result, error)) *Resolver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WithLock(path, func(c *config.Config) error {
		c.Projects["myapp"] = config.Project{Dir: "/repo/my-app"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &execx.Fake{Handler: handler}
	return &Resolver{ConfigPath: path, Runner: fake, Name: "myapp", RepoName: "my-app", Out: io.Discard}
}
