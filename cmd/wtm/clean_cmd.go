package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/stack"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

func newCleanCmd(a *app) *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:               "clean [project]",
		Short:             "Drops what removed worktrees left behind, as `wtm doctor` reports it",
		Args:              cobra.RangeArgs(0, 1),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Every registered project without an argument, where `list` would
			// take the current one: this is the counterpart of `doctor`, which
			// diagnoses the whole machine.
			names := a.cfg.Names()
			if len(args) == 1 {
				if _, err := a.cfg.Get(args[0]); err != nil {
					return err
				}
				names = []string{args[0]}
			}
			ctx := cmd.Context()
			rws := a.liveProjects(ctx, names)
			stale := a.staleIndices(rws)
			stacks := a.orphanStackNames(ctx, rws)
			volumes := a.orphanVolumeNames(ctx, rws)
			images := a.orphanImageNames(ctx, rws)
			if len(stale)+len(stacks)+len(volumes)+len(images) == 0 {
				fmt.Fprintln(a.out, "nothing to clean")
				a.reportLeftAlone(ctx, rws)
				return nil
			}
			a.printLeftovers(stale, stacks, volumes, images)
			// A closed input answers no, which is right for a person and wrong
			// for a script, so the way out is part of the message.
			if !assumeYes && !confirm(a.in, a.out, cleanQuestion(stale, stacks, volumes, images)) {
				return fmt.Errorf("cancelled: nothing was removed (pass --yes to answer for a script)")
			}

			var failed []string
			for _, s := range stale {
				o := a.options(s.Project, a.cfg.Projects[s.Project], s.Branch)
				if err := worktree.Remove(ctx, o); err != nil {
					failed = append(failed, fmt.Sprintf("  %s %s: %v", s.Project, s.Branch, err))
				}
			}
			// Releasing an index takes its stack down and sweeps the volumes and
			// images labelled with it, so part of what was scanned above is gone
			// already. Dropping that first list would fail on names docker no
			// longer holds and report a cleanup that in fact went through.
			failed = append(failed, a.downOrphanStacks(ctx, a.orphanStackNames(ctx, rws))...)
			// After the stacks: a volume a container still mounts refuses to go.
			failed = append(failed, a.drop(ctx, "volume", []string{"volume", "rm"}, a.orphanVolumeNames(ctx, rws))...)
			failed = append(failed, a.drop(ctx, "image", []string{"rmi"}, a.orphanImageNames(ctx, rws))...)
			if len(failed) > 0 {
				return fmt.Errorf("the cleanup did not finish:\n%s", strings.Join(failed, "\n"))
			}
			a.reportLeftAlone(ctx, rws)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

// reportLeftAlone accounts for what `wtm doctor` reports and this verb does not
// drop. Without it a machine holding nothing else read doctor naming a finding
// and clean answering "nothing to clean" one command later, which says the two
// disagree where in fact one of them declines on purpose.
func (a *app) reportLeftAlone(ctx context.Context, rws []repoWorktrees) {
	var lines []string
	for _, rw := range rws {
		for _, path := range rw.Abandoned {
			lines = append(lines, fmt.Sprintf("%s\n    no one can tell any more whether it holds "+
				"uncommitted work: `wtm remove %s %s --force`",
				path, rw.Name, abandonedBranch(a.cfg.Projects[rw.Name].Dir, path)))
		}
	}
	if ids := a.anonymousVolumeIDs(ctx); len(ids) > 0 {
		lines = append(lines, fmt.Sprintf("%d anonymous volume(s) no container mounts\n"+
			"    they belong to no project: `%s`", len(ids), anonymousVolumeCommand))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(a.out, "left alone on purpose, as `wtm doctor` explains at more length:")
	for _, l := range lines {
		fmt.Fprintf(a.out, "  %s\n", l)
	}
}

func (a *app) printLeftovers(stale []staleIndex, stacks []orphanStack, volumes, images []string) {
	if len(stale) > 0 {
		fmt.Fprintln(a.out, "recorded indices with no worktree behind them:")
		for _, s := range stale {
			fmt.Fprintf(a.out, "  %s: index %d, %s\n", s.Project, s.Index, s.Branch)
		}
	}
	if len(stacks) > 0 {
		fmt.Fprintln(a.out, "stacks of removed worktrees, containers included:")
		for _, s := range stacks {
			fmt.Fprintf(a.out, "  %s\n", s.Stack)
		}
	}
	for _, kind := range []struct {
		title string
		names []string
	}{{"volumes of removed worktrees:", volumes}, {"images of removed worktrees:", images}} {
		if len(kind.names) == 0 {
			continue
		}
		fmt.Fprintln(a.out, kind.title)
		for _, name := range kind.names {
			fmt.Fprintf(a.out, "  %s\n", name)
		}
	}
}

// cleanQuestion counts only the kinds actually found: naming three of them when
// one is empty reads as a wider sweep than the one about to run.
func cleanQuestion(stale []staleIndex, stacks []orphanStack, volumes, images []string) string {
	var parts []string
	if len(stale) > 0 {
		parts = append(parts, fmt.Sprintf("%d index(es)", len(stale)))
	}
	if len(stacks) > 0 {
		parts = append(parts, fmt.Sprintf("%d stack(s)", len(stacks)))
	}
	if len(volumes) > 0 {
		parts = append(parts, fmt.Sprintf("%d volume(s)", len(volumes)))
	}
	if len(images) > 0 {
		parts = append(parts, fmt.Sprintf("%d image(s)", len(images)))
	}
	return fmt.Sprintf("clean %s? (no worktree stands behind them)", strings.Join(parts, ", "))
}

// downOrphanStacks takes down the stacks of worktrees that no longer exist,
// which is the one leftover a `docker rm` alone cannot settle: the network and
// the anonymous volumes go with the project. `-p` finds the containers by
// label, so the registered project's repository only has to be a directory
// compose can run from, as it does for a stale index.
func (a *app) downOrphanStacks(ctx context.Context, orphans []orphanStack) []string {
	var failed []string
	for _, o := range orphans {
		dir := a.cfg.Projects[o.Project].Dir
		client := &stack.Client{Runner: a.runner, Dir: dir, Out: a.out}
		if err := client.Down(ctx, o.Stack, dir, true); err != nil {
			failed = append(failed, fmt.Sprintf("  stack %s: %v", o.Stack, err))
			continue
		}
		// compose down settles only what it labelled as a service of the
		// project. A container carrying the project label alone, a `compose
		// run` leftover or one started by hand on the stack's network, makes it
		// answer "No resource found to remove" and stay: measured on a stack
		// whose volume then refused to go, mounted by that very container.
		if err := a.removeLabelledContainers(ctx, o.Stack); err != nil {
			failed = append(failed, fmt.Sprintf("  containers of %s: %v", o.Stack, err))
			continue
		}
		fmt.Fprintf(a.out, "stack %s taken down\n", o.Stack)
	}
	return failed
}

// removeLabelledContainers drops whatever still carries the stack's compose
// project label, which is the only mark such a container has left.
func (a *app) removeLabelledContainers(ctx context.Context, project string) error {
	res, err := a.runner.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"ps", "-aq",
		"--filter", "label=com.docker.compose.project=" + project}})
	if err != nil {
		return err
	}
	ids := strings.Fields(res.Stdout)
	if len(ids) == 0 {
		return nil
	}
	_, err = a.runner.Run(ctx, execx.Cmd{Name: "docker",
		Args: append([]string{"rm", "--force", "--volumes"}, ids...)})
	return err
}

// drop removes one kind of leftover in a single docker call, as a worktree
// removal does. A batch that fails names its count and not the one resource
// that refused, which docker's own error already carries.
func (a *app) drop(ctx context.Context, noun string, rm, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	if _, err := a.runner.Run(ctx, execx.Cmd{Name: "docker", Args: append(rm, names...)}); err != nil {
		return []string{fmt.Sprintf("  %d %s(s): %v", len(names), noun, err)}
	}
	fmt.Fprintf(a.out, "%d %s(s) removed\n", len(names), noun)
	return nil
}
