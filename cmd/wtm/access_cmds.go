package main

import (
	"fmt"
	"io"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
	"github.com/spf13/cobra"
)

func newExecCmd(a *app) *cobra.Command {
	var service string
	cmd := &cobra.Command{
		Use:   "exec [project] <branch> -- <command>...",
		Short: "Runs a command in the worktree's application container",
		Long: "Runs a command inside the container of a worktree's stack, resolving the\n" +
			"compose project name for you.\n\n" +
			"  wtm exec feat/my-branch -- python manage.py seed_data\n" +
			"  wtm exec feat/my-branch -- bash",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 1 {
				return fmt.Errorf("separate the command with --, as in " +
					"`wtm exec <branch> -- python manage.py seed_data`")
			}
			name, p, rest, err := a.resolve(args[:dash])
			if err != nil {
				return err
			}
			return worktree.Exec(cmd.Context(), a.options(name, p, rest[0]), service, args[dash:])
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "compose service to run in (defaults to the project's app_service)")
	return cmd
}

func newRunCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "run [project] <branch> -- <command>...",
		Short: "Runs a command on the host, from the worktree directory",
		Long: "Runs a command on your machine with the worktree as working directory,\n" +
			"for editors, agents and anything else working on the files rather than\n" +
			"inside the running stack (that is what `wtm exec` is for).\n\n" +
			"  wtm run feat/my-branch -- claude\n" +
			"  wtm run feat/my-branch -- git status",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 1 {
				return fmt.Errorf("separate the command with --, as in `wtm run <branch> -- claude`")
			}
			name, p, rest, err := a.resolve(args[:dash])
			if err != nil {
				return err
			}
			return worktree.Run(cmd.Context(), a.options(name, p, rest[0]), args[dash:])
		},
	}
}

func newPathCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "path [project] <branch>",
		Short:             "Prints the worktree directory",
		Long:              "Prints the path so a shell can compose with it: `cd $(wtm path feat/my-branch)`.",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			o := a.options(name, p, rest[0])
			// This one line is meant to be substituted: `cd $(wtm path feat/x)`.
			o.Stack.Out = io.Discard
			path, err := worktree.Path(cmd.Context(), o)
			if err != nil {
				return err
			}
			fmt.Fprintln(a.out, path)
			return nil
		},
	}
}
