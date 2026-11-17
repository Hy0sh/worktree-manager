package config

import (
	"cmp"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
)

var (
	envKey      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	template    = regexp.MustCompile(`\{\{[^}]*\}\}`)
	portUse     = regexp.MustCompile(`^\{\{port:([^:{}]*):([0-9]+)\}\}$`)
	reservedEnv = []string{"COMPOSE_PROJECT_NAME", "COMPOSE_FILE", "WTM_DEPS_KEY"}
)

const runEnvForm = "{{port:<service>:<container port>}}"

// ValidateRunEnv gates run_env before it is written and again before it is used,
// since config.json can be edited by hand. wtm sets the reserved names itself.
func ValidateRunEnv(env map[string]string) error {
	for key, value := range env {
		if !envKey.MatchString(key) {
			return fmt.Errorf("invalid run_env variable name %q: use letters, digits and underscores, not starting with a digit", key)
		}
		if slices.Contains(reservedEnv, key) {
			return fmt.Errorf("run_env cannot set %s: wtm sets it itself", key)
		}
		for _, t := range template.FindAllString(value, -1) {
			m := portUse.FindStringSubmatch(t)
			if m == nil {
				return fmt.Errorf("run_env %s: unsupported %s, the only template is %s", key, t, runEnvForm)
			}
			if err := ValidateIdentifier("run_env service", m[1]); err != nil {
				return fmt.Errorf("run_env %s: %w", key, err)
			}
		}
	}
	return nil
}

// ExpandRunEnv replaces each {{port:<service>:<n>}} with the host port that
// `port` reports, and fails on one it cannot resolve.
func ExpandRunEnv(value string, port func(service, container string) (int, bool)) (string, error) {
	var err error
	out := template.ReplaceAllStringFunc(value, func(t string) string {
		m := portUse.FindStringSubmatch(t)
		if m == nil {
			err = cmp.Or(err, fmt.Errorf("unsupported %s, the only template is %s", t, runEnvForm))
			return t
		}
		p, ok := port(m[1], m[2])
		if !ok {
			err = cmp.Or(err, fmt.Errorf("no published port %s:%s", m[1], m[2]))
			return t
		}
		return strconv.Itoa(p)
	})
	return out, err
}

// identifier is the charset docker compose accepts for project and service
// names anyway. Enforced at registration, it keeps those values from reaching
// a generated shell script or YAML document as anything but a plain word.
var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidateIdentifier rejects a name that could not be a compose identifier: a
// quote or a `$(...)` in a project name lands in the restore script the
// database runs, a newline in a service name injects keys into a compose file.
func ValidateIdentifier(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if !identifier.MatchString(name) {
		return fmt.Errorf("invalid %s %q: use lowercase letters, digits, dashes and underscores, starting with a letter or a digit", kind, name)
	}
	return nil
}

// ValidateRelativePath rejects a path that could escape the directory it is
// joined under: db_path lands inside each worktree and inside the app
// container, so it has to stay a plain relative path.
func ValidateRelativePath(kind, path string) error {
	if path == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if !filepath.IsLocal(path) {
		return fmt.Errorf("invalid %s %q: must be a relative path inside the project", kind, path)
	}
	return nil
}

// ValidateCopyPattern is ValidateRelativePath for a glob, whose syntax
// filepath.Glob would otherwise only reject at the first create.
func ValidateCopyPattern(pattern string) error {
	if err := ValidateRelativePath("copy pattern", pattern); err != nil {
		return err
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid copy pattern %q: %w", pattern, err)
	}
	return nil
}
