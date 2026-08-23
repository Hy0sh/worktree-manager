package main

import (
	"context"
	"io"
	"os"
	"path/filepath"

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
	}
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
