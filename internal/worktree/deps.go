package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// lockfiles are the root files whose content decides a dependency install. A
// compose file names its dependency volumes after WTM_DEPS_KEY so worktrees on
// the same lockfile share one seeded volume instead of copying it per stack.
var lockfiles = []string{
	"pnpm-lock.yaml", "package-lock.json", "yarn.lock", "bun.lock",
	"composer.lock", "poetry.lock", "uv.lock", "Pipfile.lock", "requirements.txt",
	"Gemfile.lock", "go.sum", "Cargo.lock",
}

// depsKey hashes the lockfiles found at dir's root, "" when there is none.
func depsKey(dir string) string {
	h := sha256.New()
	found := false
	for _, name := range lockfiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		found = true
		h.Write([]byte(name + "\x00"))
		h.Write(data)
	}
	if !found {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// stackEnv is what every compose call of the stack of the worktree in dir
// interpolates with.
func stackEnv(o Options, wt stack.Worktree, dir string) []string {
	env := portEnv(o, wt)
	if key := depsKey(dir); key != "" {
		env = append(env, "WTM_DEPS_KEY="+key)
	}
	return env
}
