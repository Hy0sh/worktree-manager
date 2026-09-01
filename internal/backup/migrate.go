package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// migrate replays the migration history in a throwaway container whose memory
// cap is lifted: through `exec` the peak would land in the developer's own
// backend, where a day-to-day mem_limit OOM-kills it and the server with it.
func (m *Manager) migrate(ctx context.Context, p config.Project, cfg config.Backup, db string) error {
	files, err := compose.Files(p.Dir)
	if err != nil {
		return err
	}
	override, err := writeMemOverride(m.Root, cfg.AppService)
	if err != nil {
		return err
	}
	defer os.Remove(override)

	args := []string{"compose"}
	for _, f := range append(files, override) {
		args = append(args, "-f", f)
	}
	// A fresh container lacks anything the service installs at startup.
	shell := config.Expand(cfg.MigrateCommand, db)
	if cfg.DepsCommand != "" {
		shell = cfg.DepsCommand + " && " + shell
	}
	args = append(args, "run", "--rm", "--no-deps", "-T")
	// `-e KEY` alone makes docker read the value from its own environment.
	// Keeping the values out of the argument list keeps credentials (these are
	// often a DATABASE_URL) out of the error messages that quote the command.
	var env []string
	for _, k := range sortedKeys(cfg.Env) {
		args = append(args, "-e", k)
		env = append(env, k+"="+config.Expand(cfg.Env[k], db))
	}
	args = append(args, cfg.AppService, "sh", "-c", shell)

	if _, err := m.Runner.Run(ctx, execx.Cmd{Name: "docker", Args: args, Dir: p.Dir, Env: env, Live: true}); err != nil {
		return fmt.Errorf("migrations on the temporary database: %w", withOOMHint(err))
	}
	return nil
}

// writeMemOverride lifts the cap for the disposable container alone: mem_limit
// 0 means unlimited. The service name is re-checked because config.json can be
// hand-edited, and the file lands in wtm's 0700 tree, not the shared temp one.
func writeMemOverride(dir, service string) (string, error) {
	if err := config.ValidateIdentifier("application service", service); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "wtm-mem-*.yaml")
	if err != nil {
		return "", fmt.Errorf("temporary memory override: %w", err)
	}
	_, err = f.WriteString("services:\n  " + service + ":\n    mem_limit: 0\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// sortedKeys keeps the generated command stable across runs.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// withOOMHint turns a bare SIGKILL into something actionable. Replaying the
// whole migration history peaks well above what a shared Docker VM has left.
func withOOMHint(err error) error {
	var e *execx.Error
	if errors.As(err, &e) && e.ExitCode == 137 {
		return fmt.Errorf("%w\nprocess killed (exit 137), most likely out of memory: increase the RAM allocated to Docker, or stop non-essential services during the refresh (a frontend, a worker, an admin UI)", err)
	}
	return err
}
