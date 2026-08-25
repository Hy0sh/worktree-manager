package worktree

import (
	"context"
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
		o.logf("         replay it with `wtm exec %s -- %s`", o.Branch, o.Project.PostCreate)
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
		o.logf("         replay it with `wtm exec %s -- %s`", o.Branch, o.Project.PostCreate)
	}
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
