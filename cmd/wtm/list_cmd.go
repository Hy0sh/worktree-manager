package main

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/Hy0sh/worktree-manager/internal/worktree"
	"github.com/spf13/cobra"
)

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
				idx := "-"
				if e.Index > 0 {
					idx = strconv.Itoa(e.Index)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", idx, e.Branch, e.Status, e.Path)
			}
			return w.Flush()
		},
	}
}
