package main

import (
	"context"
	"io"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
	"github.com/spf13/cobra"
)

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
