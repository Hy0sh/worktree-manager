// Command wtm is the single entry point for the lifecycle of a project
// worktree: create, start, stop, remove, plus the pre-migrated database dump
// that makes a fresh one cheap to bootstrap.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		reportError(os.Stderr, err)
		os.Exit(1)
	}
}

// reportError adds, under the failure, where a missing binary is meant to come
// from: on macOS the docker CLI is a symlink into an application bundle, so a
// lookup failure is what installing or moving OrbStack looks like from here.
func reportError(w io.Writer, err error) {
	fmt.Fprintln(w, "error:", err)
	if execx.MissingBinary(err) == "docker" {
		fmt.Fprintln(w, "hint: every command touching a stack shells out to the docker CLI, "+
			"which ships with OrbStack and with Docker Desktop")
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
		newCreateCmd(a), newAdoptCmd(a), newListCmd(a), newStartCmd(a), newStopCmd(a), newRemoveCmd(a),
		newExecCmd(a), newRunCmd(a), newPathCmd(a),
		newProjectCmd(a), newBackupCmd(a), newDoctorCmd(a), newCleanCmd(a),
	)
	return root
}
