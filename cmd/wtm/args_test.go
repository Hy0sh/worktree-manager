package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cobra answers a missing argument by counting what it received ("accepts
// between 1 and 3 arg(s), received 0"), which names neither what is missing nor
// how to call the command. Every command taking a required positional must say
// both.
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
