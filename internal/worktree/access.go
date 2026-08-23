package worktree

import (
	"context"
	"fmt"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
)

// Exec runs a command inside the worktree's application container. Doing it by
// hand means knowing the compose project name wtc derives, which is internal
// knowledge no user should need.
func Exec(ctx context.Context, o Options, service string, command []string) error {
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	if err := o.resolveIndex(ctx, &wt, index.MustExist); err != nil {
		return err
	}
	if service == "" {
		service = o.Project.BackupConfig().AppService
	}
	if service == "" {
		return fmt.Errorf("no application service known for this project: pass --service, " +
			"or set app_service in its config")
	}
	args := append([]string{"compose", "-p", o.projectName(wt), "exec", service}, command...)
	_, err = o.Runner.Run(ctx, execx.Cmd{
		Name:        "docker",
		Args:        args,
		Dir:         wt.Path,
		Interactive: true,
	})
	return err
}

// Path returns the worktree directory, so a shell can compose with it:
// `cd $(wtm path feat/x)`.
func Path(ctx context.Context, o Options) (string, error) {
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return "", err
	}
	return wt.Path, nil
}

// Run is the counterpart of Exec: it stays on the machine, with the worktree as
// working directory, for editors, agents and anything else working on the files
// rather than in the running application.
func Run(ctx context.Context, o Options, command []string) error {
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		return err
	}
	_, err = o.Runner.Run(ctx, execx.Cmd{
		Name:        command[0],
		Args:        command[1:],
		Dir:         wt.Path,
		Interactive: true,
	})
	return err
}
