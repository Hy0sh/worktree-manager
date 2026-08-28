package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
	"github.com/spf13/cobra"
)

func newCreateCmd(a *app) *cobra.Command {
	var noStart, noPostCreate, ignoreMemory bool
	var runAfter, execAfter string
	cmd := &cobra.Command{
		Use:   "create [project] <branch> [base]",
		Short: "Creates a worktree and starts its stack",
		Long: "Creates a worktree for a registered project.\n\n" +
			"If the first argument names a registered project, it is treated as such;\n" +
			"otherwise it is a branch of the project of the current directory.\n" +
			"An existing branch is reused, local or on a remote (tracked, fetched if\n" +
			"needed), and <base> is then ignored.\n\n" +
			"--run and --exec each play a shell line once the worktree is ready, on\n" +
			"your machine and in the application container respectively. They can be\n" +
			"combined, and neither is remembered from one create to the next.\n\n" +
			"  wtm create feat/my-branch --run claude\n" +
			"  wtm create feat/my-branch --exec 'manage.py load_fixture demo'",
		Args: shellLineArgs(&runAfter, &execAfter,
			needArgs(1, 3, "name the branch to create, as in `wtm create feat/my-branch`")),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Refused before the worktree, the fetch and the restore: an --exec
			// discovered stackless at the very end is a warning nobody wanted.
			if execAfter != "" && noStart {
				return fmt.Errorf("--exec needs the stack --no-start leaves down: " +
					"drop --no-start, or use --run to play the command on your machine")
			}
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			if len(rest) > 2 {
				return fmt.Errorf("too many arguments (%s): a create takes at most "+
					"`[project] <branch> [base]`, and %q is not a registered project "+
					"(see `wtm project list`)", strings.Join(rest, " "), args[0])
			}
			o := a.options(name, p, rest[0])
			o.Base = a.cfg.BaseBranchFor(p)
			if len(rest) == 2 {
				o.Base = rest[1]
			}
			o.NoStart = noStart
			o.NoPostCreate = noPostCreate
			o.RunAfter, o.ExecAfter = runAfter, execAfter
			if ignoreMemory {
				o.Confirm = nil
			}
			if p.Dump && !noStart {
				if st := a.manager().Check(cmd.Context(), name, p); st.Behind() {
					fmt.Fprintf(a.out, "note: the dump is %s, `wtm backup refresh %s` would save the replay\n",
						st.Describe(), name)
				}
			}
			return worktree.Create(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&noStart, "no-start", false, "prepares the worktree without starting the stack")
	cmd.Flags().BoolVar(&noPostCreate, "no-post-create", false, "starts the stack without running the project's post_create")
	cmd.Flags().StringVar(&runAfter, "run", "", "shell line to play on your machine, from the worktree, once it is ready")
	cmd.Flags().StringVar(&execAfter, "exec", "", "shell line to play in the application container, after the project's post_create")
	cmd.Flags().BoolVar(&ignoreMemory, "ignore-memory", false,
		"start the stack without asking, however tight the machine's memory is")
	return cmd
}

func newStartCmd(a *app) *cobra.Command {
	var ignoreMemory bool
	cmd := &cobra.Command{
		Use:               "start [project] <branch>",
		Short:             "Starts the stack of an existing worktree",
		Args:              needArgs(1, 2, "name the branch to start, as in `wtm start feat/my-branch`"),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, branch, err := a.resolveOne(args)
			if err != nil {
				return err
			}
			o := a.options(name, p, branch)
			if ignoreMemory {
				o.Confirm = nil
			}
			return worktree.Start(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&ignoreMemory, "ignore-memory", false,
		"start the stack without asking, however tight the machine's memory is")
	return cmd
}

func newStopCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "stop [project] <branch>",
		Short: "Stops the worktree's stack, without removing it",
		Args: allArgs(&all,
			needArgs(1, 2, "name the branch to stop, as in `wtm stop feat/my-branch`")),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				name, p, entries, err := a.allWorktrees(cmd.Context(), args)
				if err != nil || len(entries) == 0 {
					return err
				}
				return a.eachWorktree(cmd.Context(), a.options(name, p, ""), entries, "stopped", worktree.Stop)
			}
			name, p, branch, err := a.resolveOne(args)
			if err != nil {
				return err
			}
			return worktree.Stop(cmd.Context(), a.options(name, p, branch))
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every worktree of the project, instead of one branch")
	return cmd
}

func newRemoveCmd(a *app) *cobra.Command {
	var force, all, assumeYes bool
	cmd := &cobra.Command{
		Use:   "remove [project] <branch>",
		Short: "Stops the stack then removes the worktree (branch kept)",
		Args: allArgs(&all,
			needArgs(1, 2, "name the branch to remove, as in `wtm remove feat/my-branch`")),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				name, p, entries, err := a.allWorktrees(cmd.Context(), args)
				if err != nil || len(entries) == 0 {
					return err
				}
				for _, e := range entries {
					fmt.Fprintf(a.out, "  %s  %s (%s)\n", e.Branch, e.Path, e.Status)
				}
				// A closed input answers no, which is right for a person and
				// wrong for a script, so the way out is part of the message.
				if !assumeYes && !confirm(a.in, a.out, fmt.Sprintf(
					"remove %d worktree(s), their stacks and volumes? (branches kept)", len(entries))) {
					return fmt.Errorf("cancelled: nothing was removed (pass --yes to answer for a script)")
				}
				o := a.options(name, p, "")
				o.Force = force
				return a.eachWorktree(cmd.Context(), o, entries, "removed", worktree.Remove)
			}
			name, p, branch, err := a.resolveOne(args)
			if err != nil {
				return err
			}
			o := a.options(name, p, branch)
			o.Force = force
			return worktree.Remove(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if tracked files are modified or the worktree is locked")
	cmd.Flags().BoolVar(&all, "all", false, "every worktree of the project, instead of one branch")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation (--all)")
	return cmd
}

// allWorktrees resolves the project --all applies to and lists what it holds.
// That form names no branch, so the project is read the way `wtm list` reads it.
func (a *app) allWorktrees(ctx context.Context, args []string) (string, config.Project, []worktree.Entry, error) {
	name, p, err := a.projectArg(args)
	if err != nil {
		return "", config.Project{}, nil, err
	}
	entries, err := worktree.List(ctx, a.options(name, p, ""))
	if err == nil && len(entries) == 0 {
		fmt.Fprintf(a.out, "no worktree for %s: nothing to do\n", name)
	}
	return name, p, entries, err
}

// eachWorktree plays action on every listed worktree, one after the other. A
// failure never stops the walk: a cleanup that gave up on the first locked
// worktree would leave every other stack running. The failures are held back
// and reported together at the end, because each worktree pours its own docker
// output over the terminal and a warning printed in the middle is a warning
// nobody reads.
func (a *app) eachWorktree(ctx context.Context, o worktree.Options, entries []worktree.Entry,
	verb string, action func(context.Context, worktree.Options) error) error {
	var failed []string
	for _, e := range entries {
		o.Branch = e.Branch
		if err := action(ctx, o); err != nil {
			failed = append(failed, fmt.Sprintf("  %s: %v", e.Branch, err))
		}
	}
	if len(failed) == 0 {
		fmt.Fprintf(a.out, "%d worktree(s) %s\n", len(entries), verb)
		return nil
	}
	return fmt.Errorf("%d of %d worktree(s) could not be %s:\n%s",
		len(failed), len(entries), verb, strings.Join(failed, "\n"))
}
