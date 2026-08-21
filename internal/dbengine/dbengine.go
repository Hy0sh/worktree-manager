// Package dbengine describes how each supported database engine is probed,
// dumped and restored. Every argv it returns runs inside the database
// container through `docker compose exec -T <service> ...`; engines needing
// the container's own variables wrap the command in `sh -c` so credentials
// expand in the container and never appear in wtm's arguments.
package dbengine

import (
	"fmt"
	"strings"
)

// Engine is one supported database engine.
type Engine interface {
	Name() string
	// DetectImage reports whether this engine runs the given image repo
	// (already lowercased and stripped of tag and digest).
	DetectImage(repo string) bool
	// ReadyArgs succeeds once the server accepts connections.
	ReadyArgs(user string) []string
	DropTempDBArgs(user, db string) []string
	// CreateTempDBArgs is nil for engines creating databases implicitly.
	CreateTempDBArgs(user, db string) []string
	// DumpArgs writes the dump of db to stdout.
	DumpArgs(user, db string) []string
	// RestoreScript is the docker-entrypoint-initdb.d body restoring the dump.
	RestoreScript(project string) string
}

var engines = []Engine{postgres{}}

// ByName resolves an engine; the empty name is postgres, which is what every
// project registered before db_engine existed means.
func ByName(name string) (Engine, error) {
	if name == "" {
		name = "postgres"
	}
	for _, e := range engines {
		if e.Name() == name {
			return e, nil
		}
	}
	return nil, fmt.Errorf("unknown database engine %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// Detect finds the engine running a compose image, e.g. "postgres:16-alpine".
func Detect(image string) (Engine, bool) {
	repo := imageRepo(image)
	if repo == "" {
		return nil, false
	}
	for _, e := range engines {
		if e.DetectImage(repo) {
			return e, true
		}
	}
	return nil, false
}

// Names lists the supported engines, for validation and completion.
func Names() []string {
	names := make([]string, 0, len(engines))
	for _, e := range engines {
		names = append(names, e.Name())
	}
	return names
}

// imageRepo strips tag and digest: "bitnami/postgresql:16" -> "bitnami/postgresql".
func imageRepo(image string) string {
	image = strings.ToLower(strings.TrimSpace(image))
	if i := strings.LastIndex(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}
	return image
}

// TempDBName keeps the throwaway database identifier valid unquoted in every
// engine: my-app would not be.
func TempDBName(project string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(project) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String() + "_snapshot_tmp"
}
