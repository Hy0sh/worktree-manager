// Command wtm is the single entry point for the lifecycle of a project
// worktree: create, start, stop, remove, plus the pre-migrated database dump
// that makes a fresh one cheap to bootstrap.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:   "wtm",
		Short: "Manages git worktrees and the docker stack of each one",
		Long: "Creates git worktrees for registered projects, copies the environment\n" +
			"files into them, restores the central database dump, and starts their\n" +
			"docker stack on its own set of ports.\n\n" +
			"Creation goes through `wtm create`: no bare invocation ever touches a\n" +
			"repository, so a typo cannot silently create a branch.",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return a.load()
		},
	}
	root.AddCommand(
		newCreateCmd(a), newListCmd(a), newStartCmd(a), newStopCmd(a), newRemoveCmd(a),
		newExecCmd(a), newRunCmd(a), newPathCmd(a),
		newProjectCmd(a), newBackupCmd(a), newDoctorCmd(a),
	)
	return root
}
