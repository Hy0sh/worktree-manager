package worktree

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Hy0sh/worktree-manager/internal/dbengine"
	"github.com/Hy0sh/worktree-manager/internal/execx"
	"github.com/Hy0sh/worktree-manager/internal/index"
	"github.com/Hy0sh/worktree-manager/internal/stack"
)

// A restore is minutes of work on a large dump, and the database only answers
// once it has finished.
const (
	dbReadyAttempts = 60
	dbReadyInterval = time.Second
)

// Installing dependencies at boot takes longer than a database answering, and
// a healthcheck adds its own start_period on top.
const appReadyAttempts = 180

// A variable, so the tests do not sleep through the wait.
var appReadyInterval = time.Second

// postCreate plays the project's post_create command in the application
// container of a brand new worktree. Every failure here is a warning and not an
// error: the worktree exists and works, and losing it over a seed that did not
// run would be the worse outcome. The message says how to replay the command.
func postCreate(ctx context.Context, o Options) {
	if o.Project.PostCreate == "" {
		return
	}
	cfg := o.Project.BackupConfig()
	if cfg.AppService == "" {
		o.logf("warning: post_create is set but no app_service is: run it with " +
			"`wtm exec " + o.Branch + " --service <service> -- ...`")
		return
	}
	wt, err := o.Stack.FindByBranch(ctx, o.Branch)
	if err != nil {
		o.logf("warning: post_create was not run: %v", err)
		return
	}
	// The index was allocated by the start that just happened, so it is in the
	// registry by now, but not in the copy of the project this call was given.
	if err := o.resolveIndex(ctx, &wt, index.MustExist); err != nil {
		o.logf("warning: post_create was not run: %v", err)
		return
	}
	if err := waitForDatabase(ctx, o, wt, cfg.DBService, cfg.DBUser, cfg.DBEngine); err != nil {
		o.logf("warning: post_create was not run: %v", err)
		o.logf("%s", replayLine(o))
		return
	}
	if err := waitForApp(ctx, o, wt, cfg.AppService); err != nil {
		o.logf("warning: post_create was not run: %v", err)
		o.logf("%s", replayLine(o))
		return
	}
	o.logf("post_create: %s", o.Project.PostCreate)
	if _, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "-p", o.projectName(wt), "exec", "-T", cfg.AppService,
			"sh", "-c", o.Project.PostCreate},
		Dir:  wt.Path,
		Live: true,
	}); err != nil {
		o.logf("warning: post_create failed: %v", err)
		o.logf("%s", replayLine(o))
	}
}

// The command runs through `sh -c`, so the line offered to the user has to as
// well: a chained post_create pasted bare would leave its tail to the user's
// own shell instead of the container.
func replayLine(o Options) string {
	return "         replay it with `wtm exec " + o.Branch + " -- sh -c " +
		execx.ShellQuote(o.Project.PostCreate) + "`"
}

// waitForApp holds until the application container reports itself healthy.
// Stacks routinely install their dependencies from the service `command:`, so
// a container docker calls started cannot run a management command yet, and a
// declared healthcheck is the only thing that knows when it can. Compose
// reports an empty health for a service without one, which is not a failure:
// running the seed early beats not running it.
func waitForApp(ctx context.Context, o Options, wt stack.Worktree, service string) error {
	probe := execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "-p", o.projectName(wt), "ps", "--format", "{{.Health}}", service},
		Dir:  wt.Path,
	}
	for i := 0; i < appReadyAttempts; i++ {
		res, err := o.Runner.Run(ctx, probe)
		if err != nil {
			return err
		}
		health := ""
		if f := strings.Fields(res.Stdout); len(f) > 0 {
			health = f[0]
		}
		switch health {
		case "healthy":
			return nil
		case "":
			o.logf("warning: %s declares no healthcheck: post_create runs as soon as the "+
				"database answers, ahead of a stack that installs at boot", service)
			return nil
		}
		// Waiting on an install can take minutes, and silence there reads as a
		// hung wtm.
		if i == 0 {
			o.logf("waiting for %s to report itself healthy", service)
		}
		if i < appReadyAttempts-1 && appReadyInterval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(appReadyInterval):
			}
		}
	}
	return fmt.Errorf("timed out waiting for %s to report itself healthy (%d attempts)",
		service, appReadyAttempts)
}

// waitForDatabase holds until the engine answers, using the same probe
// `backup refresh` uses. A project without a dump has no database wtm knows how
// to reach, and a file-based engine has no server to ask.
func waitForDatabase(ctx context.Context, o Options, wt stack.Worktree, service, user, engine string) error {
	if !o.Project.Dump || dbengine.IsFileBased(engine) {
		return nil
	}
	eng, err := dbengine.ByName(engine)
	if err != nil {
		return err
	}
	return execx.WaitFor(ctx, o.Runner, "the database of "+o.Branch, dbReadyAttempts, dbReadyInterval, execx.Cmd{
		Name: "docker",
		Args: append([]string{"compose", "-p", o.projectName(wt), "exec", "-T", service}, eng.ReadyArgs(user)...),
		Dir:  wt.Path,
	})
}
