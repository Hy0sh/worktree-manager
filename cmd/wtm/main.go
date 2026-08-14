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
	"github.com/Hy0sh/worktree-manager/internal/worktree"
	"github.com/Hy0sh/worktree-manager/internal/wtc"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
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
	root, err := a.gitToplevel()
	if err != nil {
		return "", config.Project{}, nil, err
	}
	name, p, err := a.cfg.ResolveCurrent(root)
	return name, p, args, err
}

func (a *app) gitToplevel() (string, error) {
	res, err := a.runner.Run(context.Background(), execx.Cmd{
		Name: "git",
		Args: []string{"rev-parse", "--show-toplevel"},
	})
	if err != nil {
		return "", fmt.Errorf("le dossier courant n'est pas un dépôt git: %w", err)
	}
	return strings.TrimSpace(res.Stdout), nil
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

func newRootCmd() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:   "wtm [projet] <branche> [base]",
		Short: "Crée un worktree prêt à l'emploi et démarre sa stack",
		Long: "Crée un worktree git pour un projet enregistré, y recopie les fichiers\n" +
			"d'environnement, le relie au backup Postgres central puis démarre sa stack\n" +
			"via wtc (worktree-compose), qui alloue les ports.\n\n" +
			"Si le premier argument est un projet enregistré, il est traité comme tel ;\n" +
			"sinon c'est la branche du projet du dossier courant.",
		Args:          cobra.RangeArgs(1, 3),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return a.load()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			if len(rest) > 2 {
				return fmt.Errorf("trop d'arguments: %s", strings.Join(rest, " "))
			}
			o := a.options(name, p, rest[0])
			o.Base = a.cfg.BaseBranchFor(p)
			if len(rest) == 2 {
				o.Base = rest[1]
			}
			if o.NoStart, err = cmd.Flags().GetBool("no-start"); err != nil {
				return err
			}
			return worktree.Create(cmd.Context(), o)
		},
	}
	root.Flags().Bool("no-start", false, "prépare le worktree sans démarrer la stack")
	root.AddCommand(newStopCmd(a), newRemoveCmd(a), newProjectCmd(a), newBackupCmd(a), newDoctorCmd(a))
	return root
}

func newStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:           "stop [projet] <branche>",
		Short:         "Arrête la stack du worktree, sans le supprimer",
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			return worktree.Stop(cmd.Context(), a.options(name, p, rest[0]))
		},
	}
}

func newRemoveCmd(a *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:           "remove [projet] <branche>",
		Short:         "Arrête la stack puis supprime le worktree (branche conservée)",
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
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
	cmd.Flags().BoolVar(&force, "force", false, "supprimer même si des fichiers suivis sont modifiés")
	return cmd
}

// newDoctorCmd answers "which wtc will actually run?", which stops being
// obvious once it can come from three places.
func newDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:           "doctor",
		Short:         "Diagnostique la configuration et la résolution de wtc",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(a.out, "config   %s\n", a.cfgPath)
			fmt.Fprintf(a.out, "backups  %s\n", a.backups)
			if u, err := dockermem.Read(cmd.Context(), a.runner); err == nil && u.Total > 0 {
				fmt.Fprintf(a.out, "docker   %s utilisés sur %s, %d stack(s) en cours (~%s par stack)\n",
					dockermem.Human(u.Used), dockermem.Human(u.Total), u.Projects, dockermem.Human(u.PerProject()))
				if msg := u.Warning(); msg != "" {
					fmt.Fprintln(a.out, msg)
				}
			}
			if global, err := exec.LookPath("wtc"); err == nil {
				fmt.Fprintf(a.out, "wtc global %s%s\n", global, versionSuffix(wtc.Client{Bin: global}))
			} else {
				fmt.Fprintln(a.out, "wtc global absent (`npm install -g worktree-compose` pour couvrir les projets non-Node)")
			}
			if len(a.cfg.Projects) == 0 {
				return nil
			}
			fmt.Fprintln(a.out)
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROJET\tWTC\tORIGINE\tVERSION")
			for _, name := range a.cfg.Names() {
				p := a.cfg.Projects[name]
				c := wtc.Client{Runner: a.runner, Dir: p.Dir, Out: a.out, Bin: p.WtcBin}
				r, err := c.Locate()
				if err != nil {
					fmt.Fprintf(w, "%s\tintrouvable\t\t\n", name)
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
		fmt.Fprintf(a.out, "\n%s: %d port(s) en dur dans %s, wtc ne pourra pas les isoler (%d déjà paramétré(s)).\n",
			name, len(raw), filepath.Base(base), parametrised)
		for _, line := range raw {
			fmt.Fprintf(a.out, "  %s\n", line)
		}
		fmt.Fprintln(a.out, "  Passe-les en \"${NOM_PORT:-defaut}:cible\" dans ce fichier (un override ne suffit pas, wtc ne le lit pas).")
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
		return "inconnue"
	}
	return v
}

func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Gère le registre de projets"}

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
		Use:           "create <nom>",
		Short:         "Enregistre un projet",
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
				return fmt.Errorf("%s n'est pas un répertoire accessible", abs)
			}
			if a.cfg.Has(args[0]) {
				return fmt.Errorf("le projet %q est déjà enregistré", args[0])
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
			fmt.Fprintf(a.out, "projet %s enregistré (%s)\n", args[0], abs)
			return nil
		},
	}
	create.Flags().StringVar(&dir, "dir", "", "chemin du dépôt (obligatoire)")
	create.Flags().StringVar(&base, "base", "", "branche de base du projet")
	create.Flags().BoolVar(&dump, "dump", false, "active le backup Postgres pour ce projet")
	create.Flags().BoolVar(&gitContainer, "git-container", false, "crée les symlinks .git-container (projets qui bind-montent le git-dir)")
	create.Flags().StringVar(&dbService, "db-service", "", "service compose de la base (défaut: "+config.DefaultDBService+")")
	create.Flags().StringVar(&dbUser, "db-user", "", "utilisateur postgres (défaut: "+config.DefaultDBUser+")")
	create.Flags().StringVar(&appService, "app-service", "", "service compose qui porte les migrations (ex: backend, api, php-nginx)")
	create.Flags().StringVar(&deps, "deps", "", "commande d'installation des dépendances avant migration (ex: 'poetry install --no-root --with dev')")
	create.Flags().StringVar(&migrate, "migrate", "", "commande de migration (ex: 'python manage.py migrate', 'npx prisma migrate deploy')")
	create.Flags().StringArrayVar(&env, "env", nil, "variable passée au conteneur de migration, répétable (ex: --env DB_NAME="+config.DatabasePlaceholder+")")
	_ = create.MarkFlagRequired("dir")

	list := &cobra.Command{
		Use:           "list",
		Short:         "Liste les projets enregistrés",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(a.cfg.Projects) == 0 {
				fmt.Fprintf(a.out, "aucun projet enregistré (%s)\n", a.cfgPath)
				return nil
			}
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NOM\tRÉPERTOIRE\tBASE\tDUMP")
			for _, name := range a.cfg.Names() {
				p := a.cfg.Projects[name]
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, p.Dir, a.cfg.BaseBranchFor(p), yesNo(p.Dump))
			}
			return w.Flush()
		},
	}

	var assumeYes bool
	remove := &cobra.Command{
		Use:           "remove <nom>",
		Short:         "Retire un projet du registre (worktrees et dépôt intacts)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, err := a.cfg.Get(name); err != nil {
				return err
			}
			m := a.manager()
			if _, err := os.Stat(m.DumpPath(name)); err == nil {
				if !assumeYes && !confirm(a.in, a.out, fmt.Sprintf("supprimer aussi le backup %s ?", m.DumpPath(name))) {
					return fmt.Errorf("annulé")
				}
				if _, err := m.Remove(name); err != nil {
					return err
				}
			}
			delete(a.cfg.Projects, name)
			if err := a.cfg.Save(a.cfgPath); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "projet %s retiré du registre\n", name)
			return nil
		},
	}
	remove.Flags().BoolVarP(&assumeYes, "yes", "y", false, "ne pas demander confirmation")

	cmd.AddCommand(create, list, remove)
	return cmd
}

func newBackupCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Gère le backup Postgres pré-migré des projets"}

	list := &cobra.Command{
		Use:           "list",
		Short:         "Liste les backups",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			infos, err := a.manager().List(a.cfg)
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Fprintln(a.out, "aucun projet avec backup activé")
				return nil
			}
			w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROJET\tTAILLE\tGÉNÉRÉ LE\tRÉVISION")
			for _, i := range infos {
				if !i.Present {
					fmt.Fprintf(w, "%s\taucun backup\t\t\n", i.Name)
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", i.Name, humanSize(i.Size),
					i.Meta.GeneratedAt.Format(time.RFC3339), shortRev(i.Meta.GitRev))
			}
			return w.Flush()
		},
	}

	refresh := &cobra.Command{
		Use:           "refresh [projet]",
		Short:         "Régénère le backup (démarre la stack si besoin)",
		Args:          cobra.RangeArgs(0, 1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, err := a.projectArg(args)
			if err != nil {
				return err
			}
			return a.manager().Refresh(cmd.Context(), name, p)
		},
	}

	remove := &cobra.Command{
		Use:           "remove [projet]",
		Short:         "Supprime le backup d'un projet",
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
				fmt.Fprintf(a.out, "aucun backup à supprimer pour %s\n", name)
				return nil
			}
			fmt.Fprintf(a.out, "backup de %s supprimé\n", name)
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
	root, err := a.gitToplevel()
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
			return nil, fmt.Errorf("--env attend KEY=VALUE, reçu %q", pair)
		}
		out[key] = value
	}
	return out, nil
}

func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [o/N] ", question)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "o" || answer == "oui" || answer == "y" || answer == "yes"
}

func yesNo(b bool) string {
	if b {
		return "oui"
	}
	return "non"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d o", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %co", float64(n)/float64(div), "kMG"[exp])
}

func shortRev(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}
