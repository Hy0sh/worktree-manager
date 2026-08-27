package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/gitx"
)

// needArgs answers a missing argument with the call that would have worked.
// cobra's own message ("accepts between 1 and 3 arg(s), received 0") counts
// what it got and never names what it wanted. max below zero means no ceiling.
func needArgs(min, max int, missing string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, given []string) error {
		if len(given) < min {
			return errors.New(missing)
		}
		if max >= 0 && len(given) > max {
			return fmt.Errorf("too many arguments for `%s`, see `%s --help`",
				cmd.CommandPath(), cmd.CommandPath())
		}
		return nil
	}
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
