package main

import (
	"fmt"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
	"github.com/spf13/cobra"
)

func newCreateCmd(a *app) *cobra.Command {
	var noStart bool
	cmd := &cobra.Command{
		Use:   "create [project] <branch> [base]",
		Short: "Creates a worktree and starts its stack",
		Long: "Creates a worktree for a registered project.\n\n" +
			"If the first argument names a registered project, it is treated as such;\n" +
			"otherwise it is a branch of the project of the current directory.\n" +
			"An existing branch is reused, local or on a remote (tracked, fetched if\n" +
			"needed), and <base> is then ignored.",
		Args:              needArgs(1, 3, "name the branch to create, as in `wtm create feat/my-branch`"),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			if len(rest) > 2 {
				return fmt.Errorf("too many arguments: %s", strings.Join(rest, " "))
			}
			o := a.options(name, p, rest[0])
			o.Base = a.cfg.BaseBranchFor(p)
			if len(rest) == 2 {
				o.Base = rest[1]
			}
			o.NoStart = noStart
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
	return cmd
}

func newStartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "start [project] <branch>",
		Short:             "Starts the stack of an existing worktree",
		Args:              needArgs(1, 2, "name the branch to start, as in `wtm start feat/my-branch`"),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			return worktree.Start(cmd.Context(), a.options(name, p, rest[0]))
		},
	}
}

func newStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "stop [project] <branch>",
		Short:             "Stops the worktree's stack, without removing it",
		Args:              needArgs(1, 2, "name the branch to stop, as in `wtm stop feat/my-branch`"),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			return worktree.Stop(cmd.Context(), a.options(name, p, rest[0]))
		},
	}
}

func newRemoveCmd(a *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "remove [project] <branch>",
		Short:             "Stops the stack then removes the worktree (branch kept)",
		Args:              needArgs(1, 2, "name the branch to remove, as in `wtm remove feat/my-branch`"),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			o := a.options(name, p, rest[0])
			o.Force = force
			return worktree.Remove(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if tracked files are modified or the worktree is locked")
	return cmd
}
