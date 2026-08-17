package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// errNoInput ends a stepper that has nobody to answer it: a closed stdin, a CI
// job, a command in a pipe. Asking again would spin forever on EOF.
var errNoInput = errors.New("nothing to read on the input: pass the values as flags instead")

// prompter walks the questions of a stepper. It keeps one scanner for the
// whole session, because a fresh one per question would drop what the previous
// read buffered.
type prompter struct {
	in  *bufio.Scanner
	out io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{in: bufio.NewScanner(in), out: out}
}

func (p *prompter) logf(format string, args ...any) {
	fmt.Fprintf(p.out, format+"\n", args...)
}

// ask reads one answer. An empty answer keeps current, which is what makes the
// stepper usable on an edit: enter, enter, enter, and only the field you care
// about is typed.
func (p *prompter) ask(question, current string) (string, error) {
	if current != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", question, current)
	} else {
		fmt.Fprintf(p.out, "%s: ", question)
	}
	if !p.in.Scan() {
		fmt.Fprintln(p.out)
		return "", errNoInput
	}
	answer := strings.TrimSpace(p.in.Text())
	if answer == "" {
		return current, nil
	}
	return answer, nil
}

// askRequired keeps asking until something is typed, since a stepper that
// silently registers a project without a directory helps nobody.
func (p *prompter) askRequired(question, current string) (string, error) {
	for {
		answer, err := p.ask(question, current)
		if err != nil {
			return "", err
		}
		if answer != "" {
			return answer, nil
		}
		p.logf("  this one is required")
	}
}

// askYesNo defaults to what the project already has, so an edit that skips the
// question does not silently flip a flag.
func (p *prompter) askYesNo(question string, current bool) (bool, error) {
	hint := "y/N"
	if current {
		hint = "Y/n"
	}
	fmt.Fprintf(p.out, "%s [%s] ", question, hint)
	if !p.in.Scan() {
		fmt.Fprintln(p.out)
		return false, errNoInput
	}
	switch strings.ToLower(strings.TrimSpace(p.in.Text())) {
	case "":
		return current, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// askPairs collects KEY=VALUE lines until an empty one, the shape the
// migration container's environment takes.
func (p *prompter) askPairs(question string, current map[string]string) (map[string]string, error) {
	p.logf("%s, KEY=VALUE, empty line to stop:", question)
	for k, v := range current {
		p.logf("  currently %s=%s", k, v)
	}
	pairs := map[string]string{}
	for {
		fmt.Fprint(p.out, "  ")
		if !p.in.Scan() {
			fmt.Fprintln(p.out)
			return nil, errNoInput
		}
		line := strings.TrimSpace(p.in.Text())
		if line == "" {
			if len(pairs) == 0 {
				return current, nil
			}
			return pairs, nil
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			p.logf("  expected KEY=VALUE, got %q", line)
			continue
		}
		pairs[strings.TrimSpace(key)] = value
	}
}
