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
			adoptable := 0
			for _, e := range entries {
				idx := "-"
				if e.Index > 0 {
					idx = strconv.Itoa(e.Index)
				}
				branch := e.Branch
				switch {
				// Outside wtm's own root, nothing names a detached worktree:
				// git gives no branch and the path is not one.
				case e.Detached && branch == "":
					branch = fmt.Sprintf("(detached %s)", e.ShortHead())
				case e.Detached:
					branch = fmt.Sprintf("%s (detached %s)", branch, e.ShortHead())
				}
				if e.Adoptable() {
					adoptable++
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", idx, branch, e.Status, e.Path)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if adoptable > 0 {
				fmt.Fprintf(a.out, "%d left to adopt: `wtm adopt <branch>` gives one a stack where it stands\n",
					adoptable)
			}
			return nil
		},
	}
}
