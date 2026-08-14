// Command wtm is the single entry point for the lifecycle of a
// project worktree: create, start, stop, remove, plus the Postgres backup that
// makes a fresh database cheap to bootstrap.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/backup"
	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/dockermem"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
	"github.com/Hy0sh/worktree-manager/internal/wtc"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type app struct {
	cfg     *config.Config
	cfgPath string
	backups string
	runner  execx.Runner
	out     io.Writer
	in      io.Reader
}

func (a *app) load() error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	a.cfgPath = path
	if a.cfg, err = config.Load(path); err != nil {
		return err
	}
	if a.backups, err = config.BackupsDir(); err != nil {
		return err
	}
	a.runner = execx.OSRunner{}
	a.out = os.Stdout
	a.in = os.Stdin
	return nil
}

// resolve applies the project/branch disambiguation: a first argument that
// names a registered project is the project, otherwise everything belongs to
// the project of the current directory.
func (a *app) resolve(args []string) (string, config.Project, []string, error) {
	if len(args) >= 2 && a.cfg.Has(args[0]) {
		p, err := a.cfg.Get(args[0])
		return args[0], p, args[1:], err
	}
	root, err := gitx.RepoRoot(context.Background(), a.runner)
	if err != nil {
		return "", config.Project{}, nil, err
	}
	name, p, err := a.cfg.ResolveCurrent(root)
	return name, p, args, err
}

func (a *app) options(name string, p config.Project, branch string) worktree.Options {
	return worktree.Options{
		Name:       name,
		Project:    p,
		Branch:     branch,
		BackupsDir: a.backups,
		Runner:     a.runner,
		Out:        a.out,
		Wtc:        &wtc.Client{Runner: a.runner, Dir: p.Dir, Out: a.out, Bin: p.WtcBin},
	}
}

func (a *app) manager() *backup.Manager {
	return &backup.Manager{Runner: a.runner, Root: a.backups, Out: a.out}
}

// ensureLoaded backs the completion helpers: cobra does not run
// PersistentPreRunE for the hidden __complete command, so the registry has to
// be loaded on demand. A failure just means no suggestions.
func (a *app) ensureLoaded() bool {
	if a.cfg != nil {
		return true
	}
	return a.load() == nil
}

// completeProjects suggests registered project names.
func (a *app) completeProjects(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !a.ensureLoaded() || len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.cfg.Names(), cobra.ShellCompDirectiveNoFileComp
}

// completeTargets suggests what `stop` and `remove` accept: a project name, or
// a branch that actually has a worktree. Guessing branch names by hand is
// exactly the friction worth removing here.
func (a *app) completeTargets(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !a.ensureLoaded() {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	switch {
	case len(args) == 0:
		return append(a.cfg.Names(), a.worktreeBranches("")...), cobra.ShellCompDirectiveNoFileComp
	case len(args) == 1 && a.cfg.Has(args[0]):
		return a.worktreeBranches(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// worktreeBranches lists the branches that currently have a worktree, for the
// named project or for the one of the current directory.
func (a *app) worktreeBranches(name string) []string {
	var p config.Project
	var err error
	if name != "" {
		p, err = a.cfg.Get(name)
	} else {
		var root string
		if root, err = gitx.RepoRoot(context.Background(), a.runner); err == nil {
			_, p, err = a.cfg.ResolveCurrent(root)
		}
	}
	if err != nil {
		return nil
	}
	client := &wtc.Client{Runner: a.runner, Dir: p.Dir, Out: io.Discard, Bin: p.WtcBin}
	worktrees, err := client.Worktrees(context.Background())
	if err != nil {
		return nil
	}
	branches := make([]string, 0, len(worktrees))
	for _, w := range worktrees {
		branches = append(branches, w.Branch)
	}
	return branches
}

func newRootCmd() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:   "wtm",
		Short: "Manages git worktrees and the docker stack of each one",
		Long: "Creates git worktrees for registered projects, copies the environment\n" +
			"files into them, restores the central Postgres dump, and starts their\n" +
			"stack via wtc (worktree-compose), which allocates the ports.\n\n" +
			"Creation goes through `wtm create`: no bare invocation ever touches a\n" +
			"repository, so a typo cannot silently create a branch.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return a.load()
		},
	}
	root.AddCommand(
		newCreateCmd(a), newListCmd(a), newStartCmd(a), newStopCmd(a), newRemoveCmd(a),
		newExecCmd(a), newProjectCmd(a), newBackupCmd(a), newDoctorCmd(a),
	)
	return root
}

func newCreateCmd(a *app) *cobra.Command {
	var noStart bool
	cmd := &cobra.Command{
		Use:   "create [project] <branch> [base]",
		Short: "Creates a worktree and starts its stack",
		Long: "Creates a worktree for a registered project.\n\n" +
			"If the first argument names a registered project, it is treated as such;\n" +
			"otherwise it is a branch of the project of the current directory.\n" +
			"An existing local branch is reused, and <base> is then ignored.",
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
			client := &wtc.Client{Runner: a.runner, Dir: p.Dir, Out: a.out, Bin: p.WtcBin}
			worktrees, err := client.Worktrees(cmd.Context())
			if err != nil {
				return err
			}
			if len(worktrees) == 0 {
				fmt.Fprintf(a.out, "no worktree for %s (create one with `wtm create <branch>`)\n", name)
				return nil
			}
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "INDEX\tBRANCH\tPATH")
			for _, wt := range worktrees {
				fmt.Fprintf(w, "%d\t%s\t%s\n", wt.Index, wt.Branch, wt.Path)
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

// newDoctorCmd answers "which wtc will actually run?", which stops being
// obvious once it can come from three places.
func newDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:           "doctor",
		Short:         "Diagnoses the configuration and wtc resolution",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(a.out, "config   %s\n", a.cfgPath)
			fmt.Fprintf(a.out, "backups  %s\n", a.backups)
			if u, err := dockermem.Read(cmd.Context(), a.runner); err == nil && u.Total > 0 {
				fmt.Fprintf(a.out, "docker   %s used out of %s, %d stack(s) running (~%s per stack)\n",
					dockermem.Human(u.Used), dockermem.Human(u.Total), u.Projects, dockermem.Human(u.PerProject()))
				if msg := u.Warning(); msg != "" {
					fmt.Fprintln(a.out, msg)
				}
			}
			if global, err := exec.LookPath("wtc"); err == nil {
				fmt.Fprintf(a.out, "wtc global %s%s\n", global, versionSuffix(wtc.Client{Bin: global}))
			} else {
				fmt.Fprintln(a.out, "wtc global missing (`npm install -g worktree-compose` to cover non-Node projects)")
			}
			if len(a.cfg.Projects) == 0 {
				return nil
			}
			fmt.Fprintln(a.out)
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROJECT\tWTC\tORIGIN\tVERSION")
			for _, name := range a.cfg.Names() {
				p := a.cfg.Projects[name]
				c := wtc.Client{Runner: a.runner, Dir: p.Dir, Out: a.out, Bin: p.WtcBin}
				r, err := c.Locate()
				if err != nil {
					fmt.Fprintf(w, "%s\tnot found\t\t\n", name)
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, r.Path, r.Origin, orUnknown(r.Version))
			}
			if err := w.Flush(); err != nil {
				return err
			}
			return a.reportPorts()
		},
	}
}

// reportPorts flags projects whose ports wtc cannot isolate. Parametrising them
// in an override would not help: wtc reads only the base compose file, so the
// change has to land there.
func (a *app) reportPorts() error {
	for _, name := range a.cfg.Names() {
		base, err := compose.Base(a.cfg.Projects[name].Dir)
		if err != nil {
			continue
		}
		raw, parametrised, err := compose.Ports(base)
		if err != nil || len(raw) == 0 {
			continue
		}
		fmt.Fprintf(a.out, "\n%s: %d port(s) hardcoded in %s, wtc will not be able to isolate them (%d already parametrised).\n",
			name, len(raw), filepath.Base(base), parametrised)
		for _, line := range raw {
			fmt.Fprintf(a.out, "  %s\n", line)
		}
		fmt.Fprintln(a.out, "  Turn them into \"${PORT_NAME:-default}:target\" in this file (an override is not enough, wtc does not read it).")
	}
	return nil
}

func versionSuffix(c wtc.Client) string {
	r, err := c.Locate()
	if err != nil || r.Version == "" {
		return ""
	}
	return " (" + r.Version + ")"
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

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
			p := config.Project{Dir: abs, BaseBranch: base, Dump: dump, GitContainer: gitContainer}
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
			fmt.Fprintln(w, "PROJECT\tSIZE\tGENERATED AT\tREVISION")
			for _, i := range infos {
				if !i.Present {
					fmt.Fprintf(w, "%s\tno backup\t\t\n", i.Name)
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", i.Name, humanSize(i.Size),
					i.Meta.GeneratedAt.Format(time.RFC3339), shortRev(i.Meta.GitRev))
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

// projectArg reads an explicit project name, or falls back to the current one.
func (a *app) projectArg(args []string) (string, config.Project, error) {
	if len(args) == 1 {
		p, err := a.cfg.Get(args[0])
		return args[0], p, err
	}
	root, err := gitx.RepoRoot(context.Background(), a.runner)
	if err != nil {
		return "", config.Project{}, err
	}
	return a.cfg.ResolveCurrent(root)
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

func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [y/N] ", question)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMG"[exp])
}

func shortRev(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}
