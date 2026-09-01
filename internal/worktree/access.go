package worktree

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// Exec runs a command inside the worktree's application container. Doing it by
// hand means knowing the compose project name wtm derives, which is internal
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
	return runIn(ctx, o, wt, execx.Cmd{Name: command[0], Args: command[1:]})
}

func runIn(ctx context.Context, o Options, wt stack.Worktree, c execx.Cmd) error {
	c.Dir = wt.Path
	c.Env = composeEnv(o, wt)
	c.Interactive = true
	_, err := o.Runner.Run(ctx, c)
	return err
}

// runAfter plays the command of `create --run` on the host, once everything
// else is done. Like post_create, a failure is a warning and not an error: the
// worktree is created, and the command is one `wtm run` away.
func runAfter(ctx context.Context, o Options) {
	if o.RunAfter == "" {
		return
	}
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		o.logf("warning: --run was not played: %v", err)
		return
	}
	// The index was allocated by the start that just happened, so it is in the
	// registry but not in the copy of the project this call was given. Under
	// --no-start nothing allocated one, and a host command needs no stack.
	if !o.NoStart && hasCompose(o.Project.Dir) {
		if err := o.resolveIndex(ctx, &wt, index.MustExist); err != nil {
			o.logf("warning: the compose environment is not set: %v", err)
		}
	}
	o.logf("run: %s", o.RunAfter)
	if err := runIn(ctx, o, wt, execx.Cmd{Name: "sh", Args: []string{"-c", o.RunAfter}}); err != nil {
		o.logf("warning: --run failed: %v", err)
		o.logf("         replay it with `wtm run %s -- sh -c %s`",
			execx.ShellQuote(o.Branch), execx.ShellQuote(o.RunAfter))
	}
}

// composeEnv makes a project's own `docker compose` line address the worktree's
// stack instead of one named after the directory it runs from. The file list
// counts too: wtm's overrides are not named `override`, so compose skips them.
func composeEnv(o Options, wt stack.Worktree) []string {
	if !hasCompose(o.Project.Dir) {
		return nil
	}
	// The registry and not the resolver: `wtm run` works with docker stopped,
	// and a recorded index is what says a stack was once started here. A caller
	// holding a freshly resolved one passes it in instead.
	if wt.Index <= 0 {
		wt.Index = o.Project.WorktreeIndices[o.Branch]
	}
	if wt.Index <= 0 {
		return nil
	}
	files, err := composeFiles(o, wt.Path)
	if err != nil {
		return nil
	}
	return []string{
		"COMPOSE_PROJECT_NAME=" + o.projectName(wt),
		"COMPOSE_FILE=" + strings.Join(files, string(os.PathListSeparator)),
	}
}
