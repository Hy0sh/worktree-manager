// Command wtm is the single entry point for the lifecycle of a
// project worktree: create, start, stop, remove, plus the Postgres backup that
// makes a fresh database cheap to bootstrap.
package main

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newBackupCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Manages the pre-migrated Postgres backup of projects"}

	list := &cobra.Command{
		Use:           "list",
		Short:         "Lists the backups",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			infos, err := a.manager().List(a.cfg)
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Fprintln(a.out, "no project with backup enabled")
				return nil
			}
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROJECT\tSIZE\tGENERATED AT\tREVISION\tMIGRATIONS")
			for _, i := range infos {
				if !i.Present {
					fmt.Fprintf(w, "%s\tno backup\t\t\t\n", i.Name)
					continue
				}
				staleness := "unknown"
				if p, err := a.cfg.Get(i.Name); err == nil {
					staleness = a.manager().Check(cmd.Context(), i.Name, p).Describe()
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", i.Name, humanSize(i.Size),
					i.Meta.GeneratedAt.Format(time.RFC3339), shortRev(i.Meta.GitRev), staleness)
			}
			return w.Flush()
		},
	}

	refresh := &cobra.Command{
		Use:               "refresh [project]",
		ValidArgsFunction: a.completeProjects,
		Short:             "Regenerates the backup (starts the stack if needed)",
		Args:              cobra.RangeArgs(0, 1),
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, err := a.projectArg(args)
			if err != nil {
				return err
			}
			return a.manager().Refresh(cmd.Context(), name, p)
		},
	}

	remove := &cobra.Command{
		Use:           "remove [project]",
		Short:         "Deletes a project's backup",
		Args:          cobra.RangeArgs(0, 1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := a.projectArg(args)
			if err != nil {
				return err
			}
			removed, err := a.manager().Remove(name)
			if err != nil {
				return err
			}
			if !removed {
				fmt.Fprintf(a.out, "no backup to delete for %s\n", name)
				return nil
			}
			fmt.Fprintf(a.out, "backup of %s deleted\n", name)
			return nil
		},
	}

	cmd.AddCommand(list, refresh, remove)
	return cmd
}
