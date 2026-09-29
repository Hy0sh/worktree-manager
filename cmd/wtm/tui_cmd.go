package main

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/tui"
	"github.com/Hy0sh/worktree-manager/internal/worktree"
)

func newTUICmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "tui [project]",
		Short: "Opens a live dashboard of the project's worktrees",
		Long: "Shows what `wtm list` prints, refreshed every five seconds, with the\n" +
			"addresses of the selected worktree: a terminal to leave open beside the\n" +
			"agents. r refreshes at once, q quits.",
		Args:              cobra.RangeArgs(0, 1),
		ValidArgsFunction: a.completeProjects,
		SilenceUsage:      true,
		SilenceErrors:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := a.projectArg(args)
			if err != nil {
				return err
			}
			if !isTerminal(a.in) || !isTerminal(a.out) {
				return errors.New("wtm tui needs a terminal: `wtm list` answers the same question for a script")
			}
			m := tui.New(cmd.Context(), name, a.dashboardSource(name))
			_, err = tea.NewProgram(m, tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
}

// dashboardSource reloads the registry on every question: a dashboard stays
// open all day, and an adoption made from another terminal changes what it lists.
func (a *app) dashboardSource(name string) tui.Source {
	options := func(branch string) (worktree.Options, error) {
		cfg, err := config.Load(a.cfgPath)
		if err != nil {
			return worktree.Options{}, err
		}
		p, err := cfg.Get(name)
		if err != nil {
			return worktree.Options{}, err
		}
		o := a.options(name, p, branch)
		// The screen belongs to the dashboard, and a note printed across it tears it.
		o.Out, o.Stack.Out, o.Resolver.Out = io.Discard, io.Discard, io.Discard
		return o, nil
	}
	return tui.Source{
		List: func(ctx context.Context) ([]worktree.Entry, error) {
			o, err := options("")
			if err != nil {
				return nil, err
			}
			return worktree.List(ctx, o)
		},
		Ports: func(ctx context.Context, branch string) ([]string, error) {
			o, err := options(branch)
			if err != nil {
				return nil, err
			}
			return worktree.Ports(ctx, o)
		},
	}
}
