package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/execx"
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
			volumes := a.orphanVolumeNames(ctx, rws)
			images := a.orphanImageNames(ctx, rws)
			if len(stale)+len(volumes)+len(images) == 0 {
				fmt.Fprintln(a.out, "nothing to clean")
				return nil
			}
			a.printLeftovers(stale, volumes, images)
			// A closed input answers no, which is right for a person and wrong
			// for a script, so the way out is part of the message.
			if !assumeYes && !confirm(a.in, a.out, cleanQuestion(stale, volumes, images)) {
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
			failed = append(failed, a.drop(ctx, "volume", []string{"volume", "rm"}, a.orphanVolumeNames(ctx, rws))...)
			failed = append(failed, a.drop(ctx, "image", []string{"rmi"}, a.orphanImageNames(ctx, rws))...)
			if len(failed) > 0 {
				return fmt.Errorf("the cleanup did not finish:\n%s", strings.Join(failed, "\n"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (a *app) printLeftovers(stale []staleIndex, volumes, images []string) {
	if len(stale) > 0 {
		fmt.Fprintln(a.out, "recorded indices with no worktree behind them:")
		for _, s := range stale {
			fmt.Fprintf(a.out, "  %s: index %d, %s\n", s.Project, s.Index, s.Branch)
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
func cleanQuestion(stale []staleIndex, volumes, images []string) string {
	var parts []string
	if len(stale) > 0 {
		parts = append(parts, fmt.Sprintf("%d index(es)", len(stale)))
	}
	if len(volumes) > 0 {
		parts = append(parts, fmt.Sprintf("%d volume(s)", len(volumes)))
	}
	if len(images) > 0 {
		parts = append(parts, fmt.Sprintf("%d image(s)", len(images)))
	}
	return fmt.Sprintf("clean %s? (no worktree stands behind them)", strings.Join(parts, ", "))
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
