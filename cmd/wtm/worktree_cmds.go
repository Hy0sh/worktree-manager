package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
	"github.com/spf13/cobra"
)

// afterFlags are what create and adopt both offer once the worktree stands: the
// stack may stay down, the seed may be skipped, and a shell line may play on
// either side. The verbs differ in how the worktree appears, never in what
// happens to it next.
type afterFlags struct {
	noStart      bool
	noPostCreate bool
	run          string
	exec         string
	ignoreMemory bool
}

func (f *afterFlags) bind(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.noStart, "no-start", false, "prepares the worktree without starting the stack")
	cmd.Flags().BoolVar(&f.noPostCreate, "no-post-create", false, "starts the stack without running the project's post_create")
	cmd.Flags().StringVar(&f.run, "run", "", "shell line to play on your machine, from the worktree, once it is ready")
	cmd.Flags().StringVar(&f.exec, "exec", "", "shell line to play in the application container, after the project's post_create")
	cmd.Flags().BoolVar(&f.ignoreMemory, "ignore-memory", false,
		"start the stack without asking, however tight the machine's memory is")
}

// args adds the shell-line check to a verb's own positional form.
func (f *afterFlags) args(form cobra.PositionalArgs) cobra.PositionalArgs {
	return shellLineArgs(&f.run, &f.exec, form)
}

// refuse names the one combination that cannot work, before anything is
// resolved: an --exec discovered stackless at the very end is a warning nobody
// wanted.
func (f *afterFlags) refuse() error {
	if f.exec != "" && f.noStart {
		return fmt.Errorf("--exec needs the stack --no-start leaves down: " +
			"drop --no-start, or use --run to play the command on your machine")
	}
	return nil
}

func (f *afterFlags) applyTo(o *worktree.Options) {
	o.NoStart, o.NoPostCreate = f.noStart, f.noPostCreate
	o.RunAfter, o.ExecAfter = f.run, f.exec
	if f.ignoreMemory {
		o.Confirm = nil
	}
}

func newCreateCmd(a *app) *cobra.Command {
	var flags afterFlags
	cmd := &cobra.Command{
		Use:   "create [project] <branch> [base]",
		Short: "Creates a worktree and starts its stack",
		Long: "Creates a worktree for a registered project.\n\n" +
			"If the first argument names a registered project, it is treated as such;\n" +
			"otherwise it is a branch of the project of the current directory.\n" +
			"An existing branch is reused, local or on a remote (tracked, fetched if\n" +
			"needed), and <base> is then ignored.\n\n" +
			"--run and --exec each play a shell line once the worktree is ready, on\n" +
			"your machine and in the application container respectively. They can be\n" +
			"combined, and neither is remembered from one create to the next.\n\n" +
			"  wtm create feat/my-branch --run claude\n" +
			"  wtm create feat/my-branch --exec 'manage.py load_fixture demo'",
		Args: flags.args(
			needArgs(1, 3, "name the branch to create, as in `wtm create feat/my-branch`")),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := flags.refuse(); err != nil {
				return err
			}
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			if len(rest) > 2 {
				return fmt.Errorf("too many arguments (%s): a create takes at most "+
					"`[project] <branch> [base]`, and %q is not a registered project "+
					"(see `wtm project list`)", strings.Join(rest, " "), args[0])
			}
			o := a.options(name, p, rest[0])
			o.Base = a.cfg.BaseBranchFor(p)
			if len(rest) == 2 {
				o.Base = rest[1]
			}
			flags.applyTo(&o)
			if p.Dump && !flags.noStart {
				if st := a.manager().Check(cmd.Context(), name, p); st.Behind() {
					fmt.Fprintf(a.out, "note: the dump is %s, `wtm backup refresh %s` would save the replay\n",
						st.Describe(), name)
				}
			}
			return worktree.Create(cmd.Context(), o)
		},
	}
	flags.bind(cmd)
	return cmd
}

func newStartCmd(a *app) *cobra.Command {
	var ignoreMemory bool
	cmd := &cobra.Command{
		Use:               "start [project] <branch>",
		Short:             "Starts the stack of an existing worktree",
		Args:              needArgs(1, 2, "name the branch to start, as in `wtm start feat/my-branch`"),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, p, branch, err := a.resolveOne(args)
			if err != nil {
				return err
			}
			o := a.options(name, p, branch)
			if ignoreMemory {
				o.Confirm = nil
			}
			return worktree.Start(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&ignoreMemory, "ignore-memory", false,
		"start the stack without asking, however tight the machine's memory is")
	return cmd
}

func newAdoptCmd(a *app) *cobra.Command {
	var flags afterFlags
	var assumeYes bool
	var renameTo string
	cmd := &cobra.Command{
		Use:   "adopt [project] [branch]",
		Short: "Gives an existing worktree a stack, without moving it",
		Long: "Adopts a worktree wtm did not create, a `claude --worktree` one or any\n" +
			"`git worktree add`, and gives it what a created one gets: a stable index,\n" +
			"remapped ports, provisioned .env files and a stack on the restored dump.\n\n" +
			"Without a branch, the worktree of the current directory is adopted. The\n" +
			"worktree never moves: something may well be working in it right now.\n\n" +
			"--as renames the branch on the way in, which `claude --worktree` names for\n" +
			"itself. Adopting is the moment for it: the branch is part of the compose\n" +
			"project name, so a rename once the stack exists orphans it.\n\n" +
			"Everything create offers once the worktree stands is offered here too:\n" +
			"--no-start, --no-post-create, --run and --exec behave exactly the same.\n\n" +
			"  cd ~/dev/myapp/.claude/worktrees/curry && wtm adopt\n" +
			"  wtm adopt --as feat/my-feature\n" +
			"  wtm adopt myapp worktree-curry --exec 'manage.py seed_data'",
		Args: flags.args(
			needArgs(0, 2, "adopt takes at most `[project] [branch]`")),
		ValidArgsFunction: a.completeAdoptable,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := flags.refuse(); err != nil {
				return err
			}
			name, p, rest, err := a.resolve(args)
			if err != nil {
				return err
			}
			if len(rest) > 1 {
				return fmt.Errorf("too many arguments (%s): an adopt takes at most "+
					"`[project] [branch]`, and %q is not a registered project "+
					"(see `wtm project list`)", strings.Join(rest, " "), args[0])
			}
			branch := ""
			if len(rest) == 1 {
				branch = rest[0]
			}
			o := a.options(name, p, branch)
			flags.applyTo(&o)
			o.RenameTo = renameTo
			// Deliberately not a.confirmer(): with nobody to answer, adopting
			// must refuse and name -y rather than write into somebody's
			// checkout unasked, which is what a nil Confirm would mean.
			o.ConfirmAdopt = func(question string) bool {
				return assumeYes || confirm(a.in, a.out, question)
			}
			return worktree.Adopt(cmd.Context(), o)
		},
	}
	cmd.Flags().StringVar(&renameTo, "as", "", "rename the branch to this on the way in")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation")
	flags.bind(cmd)
	return cmd
}

func newStopCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "stop [project] <branch>",
		Short: "Stops the worktree's stack, without removing it",
		Args: allArgs(&all,
			needArgs(1, 2, "name the branch to stop, as in `wtm stop feat/my-branch`")),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				name, p, entries, err := a.allWorktrees(cmd.Context(), args)
				if err != nil || len(entries) == 0 {
					return err
				}
				return a.eachWorktree(cmd.Context(), a.options(name, p, ""), entries, "stopped", worktree.Stop)
			}
			name, p, branch, err := a.resolveOne(args)
			if err != nil {
				return err
			}
			return worktree.Stop(cmd.Context(), a.options(name, p, branch))
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every worktree of the project, instead of one branch")
	return cmd
}

func newRemoveCmd(a *app) *cobra.Command {
	var force, all, assumeYes bool
	cmd := &cobra.Command{
		Use:   "remove [project] <branch>",
		Short: "Stops the stack then removes the worktree (branch kept)",
		Args: allArgs(&all,
			needArgs(1, 2, "name the branch to remove, as in `wtm remove feat/my-branch`")),
		ValidArgsFunction: a.completeTargets,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				name, p, entries, err := a.allWorktrees(cmd.Context(), args)
				if err != nil || len(entries) == 0 {
					return err
				}
				for _, e := range entries {
					fmt.Fprintf(a.out, "  %s  %s (%s)\n", e.Branch, e.Path, e.Status)
				}
				// A closed input answers no, which is right for a person and
				// wrong for a script, so the way out is part of the message.
				if !assumeYes && !confirm(a.in, a.out, removeAllQuestion(entries)) {
					return fmt.Errorf("cancelled: nothing was removed (pass --yes to answer for a script)")
				}
				o := a.options(name, p, "")
				o.Force = force
				return a.eachWorktree(cmd.Context(), o, entries, "removed", worktree.Remove)
			}
			name, p, branch, err := a.resolveOne(args)
			if err != nil {
				return err
			}
			o := a.options(name, p, branch)
			o.Force = force
			return worktree.Remove(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if tracked files are modified or the worktree is locked")
	cmd.Flags().BoolVar(&all, "all", false, "every worktree of the project, instead of one branch")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation (--all)")
	return cmd
}

// removeAllQuestion says what --all is about to do. "remove 4 worktrees" stopped
// being true once adopted ones joined the listing: those keep their directory,
// and only their stack goes.
func removeAllQuestion(entries []worktree.Entry) string {
	adopted := 0
	for _, e := range entries {
		if !e.UnderRoot {
			adopted++
		}
	}
	if adopted == 0 {
		return fmt.Sprintf("remove %d worktree(s), their stacks and volumes? (branches kept)", len(entries))
	}
	return fmt.Sprintf("remove %d worktree(s) and the stacks of %d adopted one(s), "+
		"with their volumes? (branches kept, adopted directories kept)",
		len(entries)-adopted, adopted)
}

// allWorktrees resolves the project --all applies to and lists what it holds.
// That form names no branch, so the project is read the way `wtm list` reads it.
func (a *app) allWorktrees(ctx context.Context, args []string) (string, config.Project, []worktree.Entry, error) {
	name, p, err := a.projectArg(args)
	if err != nil {
		return "", config.Project{}, nil, err
	}
	entries, err := worktree.List(ctx, a.options(name, p, ""))
	if err == nil && len(entries) == 0 {
		fmt.Fprintf(a.out, "no worktree for %s: nothing to do\n", name)
	}
	return name, p, entries, err
}

// eachWorktree plays action on every listed worktree, one after the other. A
// failure never stops the walk: a cleanup that gave up on the first locked
// worktree would leave every other stack running. The failures are held back
// and reported together at the end, because each worktree pours its own docker
// output over the terminal and a warning printed in the middle is a warning
// nobody reads.
func (a *app) eachWorktree(ctx context.Context, o worktree.Options, entries []worktree.Entry,
	verb string, action func(context.Context, worktree.Options) error) error {
	var failed []string
	for _, e := range entries {
		o.Branch = e.Branch
		if err := action(ctx, o); err != nil {
			failed = append(failed, fmt.Sprintf("  %s: %v", e.Branch, err))
		}
	}
	if len(failed) == 0 {
		fmt.Fprintf(a.out, "%d worktree(s) %s\n", len(entries), verb)
		return nil
	}
	return fmt.Errorf("%d of %d worktree(s) could not be %s:\n%s",
		len(failed), len(entries), verb, strings.Join(failed, "\n"))
}
