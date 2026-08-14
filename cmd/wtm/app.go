// Command wtm is the single entry point for the lifecycle of a
// project worktree: create, start, stop, remove, plus the Postgres backup that
// makes a fresh database cheap to bootstrap.
package main

import (
	"context"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/backup"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
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

// completeProjects suggests registered project names.
func (a *app) completeProjects(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !a.ensureLoaded() || len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.cfg.Names(), cobra.ShellCompDirectiveNoFileComp
}

// completeTargets suggests what `stop` and `remove` accept: a project name, or
// a branch that actually has a worktree. Guessing branch names by hand is
// exactly the friction worth removing here.
func (a *app) completeTargets(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !a.ensureLoaded() {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	switch {
	case len(args) == 0:
		return append(a.cfg.Names(), a.worktreeBranches("")...), cobra.ShellCompDirectiveNoFileComp
	case len(args) == 1 && a.cfg.Has(args[0]):
		return a.worktreeBranches(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// worktreeBranches lists the branches that currently have a worktree, for the
// named project or for the one of the current directory.
func (a *app) worktreeBranches(name string) []string {
	var p config.Project
	var err error
	if name != "" {
		p, err = a.cfg.Get(name)
	} else {
		var root string
		if root, err = gitx.RepoRoot(context.Background(), a.runner); err == nil {
			_, p, err = a.cfg.ResolveCurrent(root)
		}
	}
	if err != nil {
		return nil
	}
	client := &stack.Client{Runner: a.runner, Dir: p.Dir, Out: io.Discard}
	worktrees, err := client.Worktrees(context.Background())
	if err != nil {
		return nil
	}
	branches := make([]string, 0, len(worktrees))
	for _, w := range worktrees {
		branches = append(branches, w.Branch)
	}
	return branches
}
