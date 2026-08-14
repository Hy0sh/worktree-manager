package config

import (
	"fmt"
	"regexp"
)

// identifier is the charset docker compose accepts for project and service
// names anyway. Enforcing it at registration is what keeps those values from
// reaching a generated shell script or a generated YAML document as anything
// other than a plain word.
var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidateIdentifier rejects a name that could not be a compose identifier.
// A quote or a `$(...)` in a project name would otherwise land in the restore
// script running inside the database container, and a newline in a service
// name would inject arbitrary keys into the generated compose file.
func ValidateIdentifier(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if !identifier.MatchString(name) {
		return fmt.Errorf("invalid %s %q: use lowercase letters, digits, dashes and underscores, starting with a letter or a digit", kind, name)
	}
	return nil
}
