package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/term"

	"github.com/Hy0sh/worktree-manager/internal/backup"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

type app struct {
	cfg     *config.Config
	cfgPath string
	backups string
	runner  execx.Runner
	out     io.Writer
	in      io.Reader
	// latestURL is the module proxy `wtm doctor` asks for the published
	// version. Empty in tests, which keeps every one of them offline.
	latestURL string
}

func (a *app) load() error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	a.cfgPath = path
	if a.cfg, err = config.Load(path); err != nil {
		return err
	}
	if a.backups, err = config.BackupsDir(); err != nil {
		return err
	}
	a.runner = execx.OSRunner{}
	a.out = os.Stdout
	a.in = os.Stdin
	a.latestURL = latestModuleURL
	return nil
}

// resolve applies the project/branch disambiguation: a first argument that
// names a registered project is the project, otherwise everything belongs to
// the project of the current directory.
func (a *app) resolve(args []string) (string, config.Project, []string, error) {
	if len(args) >= 2 && a.cfg.Has(args[0]) {
		p, err := a.cfg.Get(args[0])
		return args[0], p, args[1:], err
	}
	root, err := gitx.RepoRoot(context.Background(), a.runner)
	if err != nil {
		return "", config.Project{}, nil, err
	}
	name, p, err := a.cfg.ResolveCurrent(root)
	return name, p, args, err
}

// resolveOne is resolve for the commands that name a single branch. Dropping a
// left-over argument silently had `wtm stop myap feat/x` stop a branch called
// `myap`, then complain no such worktree existed while listing the one asked for.
func (a *app) resolveOne(args []string) (string, config.Project, string, error) {
	// Refused before the repository is even looked up: the mistake is the
	// project name, and an error about the current directory not being a
	// registered project, or about git, sends the reader somewhere else.
	if len(args) > 1 && !a.cfg.Has(args[0]) {
		return "", config.Project{}, "", fmt.Errorf("%q is not a registered project "+
			"(see `wtm project list`): with more than one argument, the first must name one", args[0])
	}
	name, p, rest, err := a.resolve(args)
	if err != nil {
		return "", config.Project{}, "", err
	}
	return name, p, rest[0], nil
}

// optionsFor is what the commands naming a single branch run on, resolve
// included: none of them has anything to say about the project it lands in.
func (a *app) optionsFor(args []string) (worktree.Options, error) {
	name, p, branch, err := a.resolveOne(args)
	if err != nil {
		return worktree.Options{}, err
	}
	return a.options(name, p, branch), nil
}

func (a *app) options(name string, p config.Project, branch string) worktree.Options {
	return worktree.Options{
		Name:       name,
		Project:    p,
		Branch:     branch,
		BackupsDir: a.backups,
		Runner:     a.runner,
		Out:        a.out,
		Stack: &stack.Client{Runner: a.runner, Dir: p.Dir, Out: a.out,
			Managed: managed(p)},
		Resolver: &index.Resolver{
			ConfigPath: a.cfgPath,
			Runner:     a.runner,
			Name:       name,
			RepoName:   filepath.Base(p.Dir),
			Out:        a.out,
		},
		Confirm: a.confirmer(),
	}
}

// managed is the set of branches the registry holds an index for, which is
// what makes an adopted worktree visible to the listing. A recorded index is
// the only mark such a worktree carries: nothing in its path says wtm knows it.
func managed(p config.Project) map[string]bool {
	if len(p.WorktreeIndices) == 0 {
		return nil
	}
	out := make(map[string]bool, len(p.WorktreeIndices))
	for branch := range p.WorktreeIndices {
		out[branch] = true
	}
	return out
}

// confirmer is nil unless a person is there to answer: wtm exists for parallel
// agents, and a `create` must never hang on a question nobody read. IsTerminal
// asks the kernel, where a file mode cannot: /dev/null is a character device too.
func (a *app) confirmer() func(string) bool {
	f, ok := a.in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return func(question string) bool { return confirm(a.in, a.out, question) }
}

// warnf is what the registry helpers report through, so what they have to say
// reaches the same writer the command prints on.
func (a *app) warnf(format string, args ...any) {
	fmt.Fprintf(a.out, format+"\n", args...)
}

func (a *app) manager() *backup.Manager {
	return &backup.Manager{Runner: a.runner, Root: a.backups, Out: a.out}
}

// ensureLoaded backs the completion helpers: cobra does not run
// PersistentPreRunE for the hidden __complete command, so the registry has to
// be loaded on demand. A failure just means no suggestions.
func (a *app) ensureLoaded() bool {
	if a.cfg != nil {
		return true
	}
	return a.load() == nil
}
