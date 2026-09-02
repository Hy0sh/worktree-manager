package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cobra answers a missing argument by counting what it received ("accepts
// between 1 and 3 arg(s), received 0"), which names neither what is missing nor
// how to call the command. Every required positional must say both.
func TestAMissingArgumentNamesTheCallThatWouldHaveWorked(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.Args == nil || !c.Runnable() {
			return
		}
		err := c.Args(c, nil)
		if err == nil {
			return // the command is fine without arguments
		}
		if strings.Contains(err.Error(), "arg(s)") {
			t.Errorf("`%s` still answers cobra's count: %v", c.CommandPath(), err)
		}
		if !strings.Contains(err.Error(), "`wtm ") {
			t.Errorf("`%s` should show a call that works, got: %v", c.CommandPath(), err)
		}
	}
	walk(newRootCmd())
}

// humanSize let exp reach 3 and then indexed "kMG", a string of three, so
// `wtm backup list` panicked on any dump of a tebibyte or more.
func TestHumanSizeSurvivesADumpOverAGibibyte(t *testing.T) {
	cases := map[int64]string{
		512:                  "512 B",
		1 << 20:              "1.0 MB",
		1 << 30:              "1.0 GB",
		1099511627776:        "1024.0 GB", // 1 TiB: no unit beyond G, so it counts up
		1024 * 1099511627776: "1048576.0 GB",
	}
	for n, want := range cases {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
