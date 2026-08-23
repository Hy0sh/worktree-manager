package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

func newProjectEditCmd(a *app) *cobra.Command {
	f := &projectFlags{}
	cmd := &cobra.Command{
		Use:   "edit <name>",
		Short: "Changes the settings of a registered project",
		Long: "Changes a registered project, the backup settings among them. Only the\n" +
			"flags actually given are touched; called without any, it walks the\n" +
			"settings one question at a time, each defaulting to the current value.\n\n" +
			"The port offset and the recorded worktree indices are never touched:\n" +
			"re-registering a project would renumber every stack it has running.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			current, err := a.cfg.Get(name)
			if err != nil {
				return err
			}
			u, err := f.update(cmd)
			if err != nil {
				return err
			}
			if u.IsEmpty() {
				if u, err = f.steppedUpdate(a, current); err != nil {
					return err
				}
			}

			warn := func(format string, args ...any) { fmt.Fprintf(a.out, format+"\n", args...) }
			var edited config.Project
			var changes []config.FieldChange
			if err := config.WithLock(a.cfgPath, func(c *config.Config) error {
				// Re-read under the lock: the edit applies to what the registry
				// holds now, not to what it held when the questions started.
				p, ok := c.Projects[name]
				if !ok {
					return fmt.Errorf("project %q is not registered any more", name)
				}
				applied, ch := u.Apply(p)
				// An edit enabling the backup by flags never saw the engine
				// question: detect it rather than defaulting to postgres
				// silently.
				detectEngineIfUnset(&applied, warn)
				edited, changes = applied, ch
				c.Projects[name] = applied
				return nil
			}); err != nil {
				return err
			}
			// Outside the lock: reading the project's compose files has no place
			// in a critical section held over a small registry file.
			warnPinnedContainers(edited, warn)
			printChanges(a, name, changes)
			return nil
		},
	}
	f.bind(cmd)
	return cmd
}
