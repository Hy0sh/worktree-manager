package worktree

import (
	"context"
	"fmt"
	"strconv"
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

// Installing dependencies at boot takes longer than a database answering, and a
// project declaring a start_period of several minutes is being reasonable: wtm
// must not give up before docker does.
const appReadyAttempts = 600

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

// waitForApp holds until the application container can answer. A healthcheck
// is the best signal, and the only one a compose file states on purpose; a
// service without one still publishes a port wtm remapped itself, and a
// listening socket is the next best thing.
func waitForApp(ctx context.Context, o Options, wt stack.Worktree, service string) error {
	health, err := appHealth(ctx, o, wt, service)
	if err != nil {
		return err
	}
	if health != "" {
		return waitUntil(ctx, o, service+" to report itself healthy", func() (bool, error) {
			h, err := appHealth(ctx, o, wt, service)
			return h == "healthy", err
		})
	}
	port := publishedPort(o, wt, service)
	if port == "" {
		o.logf("warning: %s declares no healthcheck and publishes no port: post_create "+
			"runs as soon as the database answers, ahead of a stack that installs at boot", service)
		return nil
	}
	return waitUntil(ctx, o, service+" to listen on "+port+" (it declares no healthcheck)",
		func() (bool, error) { return listening(ctx, o, wt, service, port) })
}

// appHealth is empty for a service that declares no healthcheck, which is how
// compose reports the absence of one.
func appHealth(ctx context.Context, o Options, wt stack.Worktree, service string) (string, error) {
	res, err := o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "-p", o.projectName(wt), "ps", "--format", "{{.Health}}", service},
		Dir:  wt.Path,
	})
	if err != nil {
		return "", err
	}
	if f := strings.Fields(res.Stdout); len(f) > 0 {
		return f[0], nil
	}
	return "", nil
}

// publishedPort is the container side of the service's first published port,
// the one its `command:` is expected to serve. A service publishing none (a
// queue worker) has nothing to wait on.
func publishedPort(o Options, wt stack.Worktree, service string) string {
	allocs, err := allocations(o, wt)
	if err != nil {
		return ""
	}
	for _, a := range allocs {
		if a.Service == service {
			return a.Container
		}
	}
	return ""
}

// listening asks the container's own network namespace, because the host side
// of a published port proves nothing: docker accepts a connection there while
// nothing inside listens yet. /proc/net/tcp answers without any tool the image
// may not ship.
func listening(ctx context.Context, o Options, wt stack.Worktree, service, port string) (bool, error) {
	n, err := strconv.Atoi(port)
	if err != nil {
		return false, fmt.Errorf("port %q of service %s is not a number", port, service)
	}
	// Columns are `local_address rem_address st`, where 0A is LISTEN and a
	// listener has no peer. tcp6 states the local address over 32 hex digits.
	pattern := fmt.Sprintf(":%04X [0-9A-F]+:0000 0A", n)
	_, err = o.Runner.Run(ctx, execx.Cmd{
		Name: "docker",
		Args: []string{"compose", "-p", o.projectName(wt), "exec", "-T", service,
			"sh", "-c", "grep -qE '" + pattern + "' /proc/net/tcp /proc/net/tcp6"},
		Dir: wt.Path,
	})
	return err == nil, nil
}

// waitUntil polls ready, naming what it waits on the first time the answer is
// no: minutes of silence read as a hung wtm.
func waitUntil(ctx context.Context, o Options, what string, ready func() (bool, error)) error {
	for i := 0; i < appReadyAttempts; i++ {
		ok, err := ready()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if i == 0 {
			o.logf("waiting for %s", what)
		}
		if i < appReadyAttempts-1 && appReadyInterval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(appReadyInterval):
			}
		}
	}
	return fmt.Errorf("timed out waiting for %s (%d attempts)", what, appReadyAttempts)
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
