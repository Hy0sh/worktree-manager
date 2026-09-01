package main

import (
	"context"
	"io"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
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

// completeCreate answers each position of `create` in turn: the projects, then
// nothing for the branch being created, then the bases and the flags. Once a
// base is there --from-here leaves the list, which is where it is refused too.
func (a *app) completeCreate(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !a.ensureLoaded() {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	named := len(args) > 0 && a.cfg.Has(args[0])
	project := ""
	if named {
		project = args[0]
	}
	switch {
	case len(args) == 0:
		return a.cfg.Names(), cobra.ShellCompDirectiveNoFileComp
	case len(args) == 1 && named:
		// The branch being created is a name that does not exist yet.
		return nil, cobra.ShellCompDirectiveNoFileComp
	case len(args) == 1, len(args) == 2 && named:
		return append(a.baseBranches(project), createFlags(cmd, true)...),
			cobra.ShellCompDirectiveNoFileComp
	}
	return createFlags(cmd, false), cobra.ShellCompDirectiveNoFileComp
}

// createFlags reads the flags off the command rather than listing them, which
// would go stale at the next one added. baseFree keeps --from-here out once a
// base is already named.
func createFlags(cmd *cobra.Command, baseFree bool) []string {
	var out []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || (f.Name == "from-here" && !baseFree) {
			return
		}
		out = append(out, "--"+f.Name)
	})
	return out
}

// baseBranches are what a worktree can be cut from: every local branch, plus
// the tracking refs no local branch stands for. Not worktreeBranches, which
// answers a different question, the branches that already have a worktree.
func (a *app) baseBranches(name string) []string {
	p, err := a.completionProject(name)
	if err != nil {
		return nil
	}
	res, err := a.runner.Run(context.Background(), execx.Cmd{
		Name: "git",
		Args: []string{"-C", p.Dir, "for-each-ref", "--format=%(refname)",
			"refs/heads", "refs/remotes"},
	})
	if err != nil {
		return nil
	}
	local := map[string]bool{}
	var branches, remote []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		switch ref := strings.TrimSpace(line); {
		case strings.HasPrefix(ref, "refs/heads/"):
			b := strings.TrimPrefix(ref, "refs/heads/")
			local[b] = true
			branches = append(branches, b)
		case strings.HasPrefix(ref, "refs/remotes/"):
			remote = append(remote, strings.TrimPrefix(ref, "refs/remotes/"))
		}
	}
	for _, r := range remote {
		// origin/HEAD is a symref to the default branch, never a base of its
		// own, and a remote branch a local one already stands for is noise.
		if _, short, ok := strings.Cut(r, "/"); ok && short != "HEAD" && !local[short] {
			branches = append(branches, r)
		}
	}
	return branches
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
