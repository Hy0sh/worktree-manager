package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manages the project registry"}
	cmd.AddCommand(newProjectCreateCmd(a), newProjectEditCmd(a), newProjectListCmd(a),
		newProjectProfilesCmd(a), newProjectRemoveCmd(a))
	return cmd
}

func newProjectProfilesCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "profiles [project]",
		ValidArgsFunction: a.completeProjects,
		Short:             "Lists the profiles `--profile` accepts, with what each one starts and leaves out",
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			p, err := a.completionProject(name)
			if err != nil {
				return err
			}
			all, err := compose.Services(p.Dir)
			if err != nil {
				return err
			}
			if len(p.Profiles) == 0 {
				fmt.Fprintf(a.out, "no profile: every start brings up the whole stack (%d services), "+
					"declare some with `wtm project edit <project> --profile-set light=db,backend`\n", len(all))
				return nil
			}
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			for _, profile := range p.ProfileNames() {
				named := p.Profiles[profile]
				started, err := compose.WithDependencies(p.Dir, named)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\n", profile, strings.Join(named, ", "))
				if extra := without(started, named); len(extra) > 0 {
					fmt.Fprintf(w, "\tplus, through depends_on: %s\n", strings.Join(extra, ", "))
				}
				if out := without(all, started); len(out) > 0 {
					fmt.Fprintf(w, "\tleaves out: %s\n", strings.Join(out, ", "))
				}
				if desc := p.ProfileDescriptions[profile]; desc != "" {
					fmt.Fprintf(w, "\t%s\n", desc)
				}
			}
			fmt.Fprintf(w, "(none)\tall %d services\n", len(all))
			return w.Flush()
		},
	}
}

// without keeps the order of list, which is the compose file's.
func without(list, drop []string) []string {
	var out []string
	for _, s := range list {
		if !slices.Contains(drop, s) {
			out = append(out, s)
		}
	}
	return out
}

// refuseIfWorktreesRemain keeps the removal ordered: worktree commands need the
// registry entry, and the freed offset goes to the next project, whose stacks
// would then fight those worktrees' ports. A git error traps nothing.
func refuseIfWorktreesRemain(ctx context.Context, a *app, name string, p config.Project) error {
	client := &stack.Client{Runner: a.runner, Dir: p.Dir}
	worktrees, err := client.Worktrees(ctx)
	if err != nil || len(worktrees) == 0 {
		return nil
	}
	branches := make([]string, 0, len(worktrees))
	for _, wt := range worktrees {
		branches = append(branches, wt.Branch)
	}
	return fmt.Errorf("project %s still has %d worktree(s): %s\n"+
		"remove them first with `wtm remove %s <branch>`, since their ports derive from the port offset %d "+
		"that the next registered project would reuse",
		name, len(worktrees), strings.Join(branches, ", "), name, p.PortOffset)
}

func newProjectListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "Lists the registered projects",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(a.cfg.Projects) == 0 {
				fmt.Fprintf(a.out, "no registered project (%s)\n", a.cfgPath)
				return nil
			}
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tDIRECTORY\tBASE\tDUMP")
			for _, name := range a.cfg.Names() {
				p := a.cfg.Projects[name]
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, p.Dir, a.cfg.BaseBranchFor(p), yesNo(p.Dump))
			}
			return w.Flush()
		},
	}
}

func newProjectRemoveCmd(a *app) *cobra.Command {
	var assumeYes bool
	remove := &cobra.Command{
		Use:               "remove <name>",
		ValidArgsFunction: a.completeProjects,
		Short:             "Removes a project from the registry (worktrees and repository untouched)",
		Args:              needArgs(1, 1, "name the project to remove, as in `wtm project remove my-app`"),
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			p, err := a.cfg.Get(name)
			if err != nil {
				return err
			}
			if err := refuseIfWorktreesRemain(cmd.Context(), a, name, p); err != nil {
				return err
			}
			m := a.manager()
			if _, err := os.Stat(m.DumpPath(name)); err == nil {
				// A closed input answers no, which is right for a person and
				// wrong for a script, so the way out is part of the message.
				if !assumeYes && !confirm(a.in, a.out, fmt.Sprintf("also delete the backup %s?", m.DumpPath(name))) {
					return fmt.Errorf("cancelled: nothing was removed (pass --yes to answer for a script)")
				}
				if _, err := m.Remove(name); err != nil {
					return err
				}
			}
			if err := config.WithLock(a.cfgPath, func(c *config.Config) error {
				delete(c.Projects, name)
				return nil
			}); err != nil {
				return err
			}
			delete(a.cfg.Projects, name)
			fmt.Fprintf(a.out, "project %s removed from the registry\n", name)
			return nil
		},
	}
	remove.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation")
	return remove
}

func parseEnv(pairs []string) (map[string]string, error) {
	return parsePairs("--env", "KEY=VALUE", pairs)
}

func parsePairs(flag, form string, pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("%s expects %s, got %q", flag, form, pair)
		}
		out[key] = value
	}
	return out, nil
}

// parseProfiles reads the NAME=svc,svc form. The services land on a docker
// command line, and a profile naming none would start the whole stack rather
// than nothing: both are refused here rather than at the first `start`.
func parseProfiles(pairs []string) (map[string][]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(pairs))
	for _, pair := range pairs {
		name, list, ok := strings.Cut(pair, "=")
		if !ok || name == "" || list == "" {
			return nil, fmt.Errorf("--profile-set expects NAME=service,service, got %q", pair)
		}
		services := strings.Split(list, ",")
		for _, service := range services {
			if err := config.ValidateIdentifier("compose service", service); err != nil {
				return nil, fmt.Errorf("--profile-set %q: %w", name, err)
			}
		}
		out[name] = services
	}
	return out, nil
}
