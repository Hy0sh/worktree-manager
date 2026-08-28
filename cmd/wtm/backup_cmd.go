package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newBackupCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Manages the pre-migrated database backup of projects"}

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

	var assumeYes bool
	remove := &cobra.Command{
		Use:               "remove [project]",
		Short:             "Deletes a project's backup",
		Args:              cobra.RangeArgs(0, 1),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := a.projectArg(args)
			if err != nil {
				return err
			}
			m := a.manager()
			if _, err := os.Stat(m.DumpPath(name)); err == nil {
				// Called without an argument this takes the current directory's
				// project, and the dump is the migration history wtm exists not
				// to replay: only `backup refresh` brings it back. `project
				// remove` already asks before deleting this very file. A closed
				// input answers no, which is right for a person and wrong for a
				// script, so the way out is part of the message.
				if !assumeYes && !confirm(a.in, a.out, fmt.Sprintf(
					"delete the backup of %s? only `wtm backup refresh` brings it back", name)) {
					return fmt.Errorf("cancelled: the backup of %s was kept (pass --yes to answer for a script)", name)
				}
			}
			removed, err := m.Remove(name)
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

	remove.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation")
	cmd.AddCommand(list, refresh, remove)
	return cmd
}
