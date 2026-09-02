package main

import (
	"context"
	"io"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
	"github.com/spf13/cobra"
)

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

// completeAdoptable suggests what `adopt` accepts: a project name, or a branch
// whose worktree wtm does not manage yet. Suggesting the ones it already
// manages would offer nothing but a refusal.
func (a *app) completeAdoptable(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !a.ensureLoaded() {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	switch {
	case len(args) == 0:
		return append(a.cfg.Names(), a.adoptableBranches("")...), cobra.ShellCompDirectiveNoFileComp
	case len(args) == 1 && a.cfg.Has(args[0]):
		return a.adoptableBranches(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// adoptableBranches lists the branches of the worktrees git carries and wtm
// does not manage, for the named project or for the one of the current
// directory. A detached worktree has no branch to adopt it by.
func (a *app) adoptableBranches(name string) []string {
	p, err := a.completionProject(name)
	if err != nil {
		return nil
	}
	client := &stack.Client{Runner: a.runner, Dir: p.Dir, Out: io.Discard}
	worktrees, err := client.All(context.Background())
	if err != nil {
		return nil
	}
	var branches []string
	for _, w := range worktrees {
		if !w.UnderRoot && !w.Detached && p.WorktreeIndices[w.Branch] == 0 {
			branches = append(branches, w.Branch)
		}
	}
	return branches
}

// worktreeBranches lists the branches that currently have a worktree, for the
// named project or for the one of the current directory.
func (a *app) worktreeBranches(name string) []string {
	p, err := a.completionProject(name)
	if err != nil {
		return nil
	}
	client := &stack.Client{Runner: a.runner, Dir: p.Dir, Out: io.Discard, Managed: managed(p)}
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

// completionProject resolves the project a suggestion is about: the named one,
// or the one the current directory belongs to.
func (a *app) completionProject(name string) (config.Project, error) {
	if name != "" {
		return a.cfg.Get(name)
	}
	root, err := gitx.RepoRoot(context.Background(), a.runner)
	if err != nil {
		return config.Project{}, err
	}
	_, p, err := a.cfg.ResolveCurrent(root)
	return p, err
}
