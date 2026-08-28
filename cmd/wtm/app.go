package main

import (
	"context"
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

func (a *app) options(name string, p config.Project, branch string) worktree.Options {
	return worktree.Options{
		Name:       name,
		Project:    p,
		Branch:     branch,
		BackupsDir: a.backups,
		Runner:     a.runner,
		Out:        a.out,
		Stack:      &stack.Client{Runner: a.runner, Dir: p.Dir, Out: a.out},
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

// confirmer is nil unless a person is there to answer. wtm exists for parallel
// agents, so a `create` must never hang on a question nor fail on one nobody
// read, and a terminal is the only evidence that someone will.
//
// term.IsTerminal asks the kernel, where a file mode cannot: /dev/null is a
// character device like a terminal is, so `wtm create < /dev/null`, the ordinary
// way a script calls a command, would have been asked and answered no.
func (a *app) confirmer() func(string) bool {
	f, ok := a.in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return func(question string) bool { return confirm(a.in, a.out, question) }
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
