// Command wtm is the single entry point for the lifecycle of a
// project worktree: create, start, stop, remove, plus the Postgres backup that
// makes a fresh database cheap to bootstrap.
package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
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
		Args:              cobra.RangeArgs(1, 3),
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

func newListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "list [project]",
		Short:             "Lists the worktrees of a project",
		Args:              cobra.RangeArgs(0, 1),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, err := a.projectArg(args)
			if err != nil {
				return err
			}
			entries, err := worktree.List(cmd.Context(), a.options(name, p, ""))
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintf(a.out, "no worktree for %s (create one with `wtm create <branch>`)\n", name)
				return nil
			}
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "INDEX\tBRANCH\tSTATUS\tPATH")
			for _, e := range entries {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", e.Index, e.Branch, e.Status, e.Path)
			}
			return w.Flush()
		},
	}
}

func newStartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "start [project] <branch>",
		Short:             "Starts the stack of an existing worktree",
		Args:              cobra.RangeArgs(1, 2),
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
		Args:              cobra.RangeArgs(1, 2),
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

func newRemoveCmd(a *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "remove [project] <branch>",
		Short:             "Stops the stack then removes the worktree (branch kept)",
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
			o.Force = force
			return worktree.Remove(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if tracked files are modified")
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
			path, err := worktree.Path(cmd.Context(), a.options(name, p, rest[0]))
			if err != nil {
				return err
			}
			fmt.Fprintln(a.out, path)
			return nil
		},
	}
}
