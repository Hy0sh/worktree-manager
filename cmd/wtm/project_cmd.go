// Command wtm is the single entry point for the lifecycle of a
// project worktree: create, start, stop, remove, plus the Postgres backup that
// makes a fresh database cheap to bootstrap.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/config"
)

func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manages the project registry"}

	var (
		dir          string
		base         string
		dump         bool
		gitContainer bool
		dbService    string
		dbUser       string
		appService   string
		deps         string
		migrate      string
		env          []string
	)
	create := &cobra.Command{
		Use:           "create <name>",
		Short:         "Registers a project",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.ValidateIdentifier("project name", args[0]); err != nil {
				return err
			}
			for kind, value := range map[string]string{"database service": dbService, "application service": appService} {
				if value == "" {
					continue
				}
				if err := config.ValidateIdentifier(kind, value); err != nil {
					return err
				}
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			info, err := os.Stat(abs)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("%s is not an accessible directory", abs)
			}
			if a.cfg.Has(args[0]) {
				return fmt.Errorf("project %q is already registered", args[0])
			}
			p := config.Project{
				Dir:          abs,
				BaseBranch:   base,
				Dump:         dump,
				GitContainer: gitContainer,
				PortOffset:   a.cfg.NextPortOffset(),
			}
			if dbService != "" || dbUser != "" || appService != "" || deps != "" || migrate != "" || len(env) > 0 {
				envMap, err := parseEnv(env)
				if err != nil {
					return err
				}
				p.Backup = &config.Backup{
					DBService:      dbService,
					DBUser:         dbUser,
					AppService:     appService,
					DepsCommand:    deps,
					MigrateCommand: migrate,
					Env:            envMap,
				}
			}
			a.cfg.Projects[args[0]] = p
			if err := a.cfg.Save(a.cfgPath); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "project %s registered (%s)\n", args[0], abs)
			if p.PortOffset > 0 {
				fmt.Fprintf(a.out, "ports shifted by %d so they do not clash with the other projects\n", p.PortOffset)
			}
			return nil
		},
	}
	create.Flags().StringVar(&dir, "dir", "", "path to the repository (required)")
	create.Flags().StringVar(&base, "base", "", "project's base branch")
	create.Flags().BoolVar(&dump, "dump", false, "enables the Postgres backup for this project")
	create.Flags().BoolVar(&gitContainer, "git-container", false, "creates the .git-container symlinks (projects that bind-mount the git-dir)")
	create.Flags().StringVar(&dbService, "db-service", "", "compose service for the database (default: "+config.DefaultDBService+")")
	create.Flags().StringVar(&dbUser, "db-user", "", "postgres user (default: "+config.DefaultDBUser+")")
	create.Flags().StringVar(&appService, "app-service", "", "compose service that runs the migrations (e.g. backend, api, php-nginx)")
	create.Flags().StringVar(&deps, "deps", "", "dependency install command before migration (e.g. 'poetry install --no-root --with dev')")
	create.Flags().StringVar(&migrate, "migrate", "", "migration command (e.g. 'python manage.py migrate', 'npx prisma migrate deploy')")
	create.Flags().StringArrayVar(&env, "env", nil, "variable passed to the migration container, repeatable (e.g. --env DB_NAME="+config.DatabasePlaceholder+")")
	_ = create.MarkFlagRequired("dir")

	list := &cobra.Command{
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

	var assumeYes bool
	remove := &cobra.Command{
		Use:               "remove <name>",
		ValidArgsFunction: a.completeProjects,
		Short:             "Removes a project from the registry (worktrees and repository untouched)",
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, err := a.cfg.Get(name); err != nil {
				return err
			}
			m := a.manager()
			if _, err := os.Stat(m.DumpPath(name)); err == nil {
				if !assumeYes && !confirm(a.in, a.out, fmt.Sprintf("also delete the backup %s?", m.DumpPath(name))) {
					return fmt.Errorf("cancelled")
				}
				if _, err := m.Remove(name); err != nil {
					return err
				}
			}
			delete(a.cfg.Projects, name)
			if err := a.cfg.Save(a.cfgPath); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "project %s removed from the registry\n", name)
			return nil
		},
	}
	remove.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation")

	cmd.AddCommand(create, list, remove)
	return cmd
}

// parseEnv turns repeated --env KEY=VALUE flags into a map.
func parseEnv(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--env expects KEY=VALUE, got %q", pair)
		}
		out[key] = value
	}
	return out, nil
}
