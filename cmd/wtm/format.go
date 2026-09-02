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

// allArgs accepts the `--all` form, which names a project and no branch, and
// falls back to the branch form otherwise. cobra parses the flags before it
// validates the positionals, so the flag is already read here.
func allArgs(all *bool, branchForm cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, given []string) error {
		if *all {
			return cobra.RangeArgs(0, 1)(cmd, given)
		}
		return branchForm(cmd, given)
	}
}

// shellLineArgs refuses a --run or --exec value that is really a flag. pflag
// takes whatever follows a long flag as its value, `--` and another flag
// included, so `--exec --no-start` left --no-start unset.
func shellLineArgs(run, exec *string, form cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, given []string) error {
		for _, f := range []struct {
			name  string
			value *string
		}{{"run", run}, {"exec", exec}} {
			if strings.HasPrefix(*f.value, "--") {
				return fmt.Errorf("--%s takes a shell line, not %q: quote it "+
					"(`--%s 'npm run seed'`), it is not an argv after `--`",
					f.name, *f.value, f.name)
			}
		}
		return form(cmd, given)
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
	// exp indexes "kMG" below, so it must stop at its last letter: letting it
	// reach 3 panicked on a dump of a tebibyte or more.
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 2 {
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
