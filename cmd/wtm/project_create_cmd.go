package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

func newProjectCreateCmd(a *app) *cobra.Command {
	f := &projectFlags{}
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Registers a project",
		Long: "Registers a project. Called without --dir, it asks for what it needs\n" +
			"one question at a time instead of expecting every flag to be known;\n" +
			"any flag already given becomes the answer offered by default. The\n" +
			"name is one of the questions, and defaults to the directory's own.",
		Args:          needArgs(0, 1, "name the project, as in `wtm project create my-app`"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
				if err := config.ValidateIdentifier("project name", name); err != nil {
					return err
				}
			}
			u, err := f.update(cmd)
			if err != nil {
				return err
			}
			// The repository is the one thing a project cannot do without, so
			// its absence is what tells a scripted call from a bare one.
			p, _ := u.Apply(config.Project{})
			if u.Dir == nil {
				stepped, err := f.steppedUpdate(a, p)
				if err != nil {
					return err
				}
				p, _ = stepped.Apply(p)
			}
			// Asked once the directory is known, whether it came from a flag or
			// from the stepper, because that is what the answer defaults to.
			if name == "" {
				if f.noInput {
					return fmt.Errorf("name the project, as in `wtm project create my-app`, " +
						"or drop --no-input to be asked")
				}
				if name, err = askName(newPrompter(a.in, a.out), filepath.Base(p.Dir)); err != nil {
					return err
				}
			}
			// The stepper asks for the engine; a flags-only registration gets
			// the same compose-image detection here.
			warn := func(format string, args ...any) { fmt.Fprintf(a.out, format+"\n", args...) }
			detectEngineIfUnset(&p, warn)
			warnPinnedContainers(p, warn)

			// The existence check and the offset both read the registry, so
			// they live inside the same lock as the write: two concurrent
			// registrations must not pick the same offset.
			if err := config.WithLock(a.cfgPath, func(c *config.Config) error {
				if c.Has(name) {
					return fmt.Errorf("project %q is already registered (`wtm project edit %s` to change it)", name, name)
				}
				p.PortOffset = c.NextPortOffset()
				c.Projects[name] = p
				return nil
			}); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "project %s registered (%s)\n", name, p.Dir)
			if p.PortOffset > 0 {
				fmt.Fprintf(a.out, "ports shifted by %d so they do not clash with the other projects\n", p.PortOffset)
			}
			return nil
		},
	}
	f.bind(cmd)
	return cmd
}
