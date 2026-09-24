package compose

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ServiceEnvironment is a service's `environment:` across the compose files, a
// later file winning, in either syntax (a mapping, or a list of KEY=VALUE).
// "${VAR:-default}" reads as its default; "${VAR}" is kept as written, since
// its value lives in a shell or an .env file wtm does not read.
func ServiceEnvironment(dir, service string) map[string]string {
	files, err := Files(dir)
	if err != nil {
		return nil
	}
	env := map[string]string{}
	for _, path := range files {
		services, err := servicesMapping(path)
		if err != nil {
			continue
		}
		block := mapValue(mapValue(services, service), "environment")
		if block == nil {
			continue
		}
		switch block.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(block.Content); i += 2 {
				env[block.Content[i].Value] = interpolated(deref(block.Content[i+1]).Value)
			}
		case yaml.SequenceNode:
			for _, item := range block.Content {
				if key, value, ok := strings.Cut(deref(item).Value, "="); ok {
					env[key] = interpolated(value)
				}
			}
		}
	}
	return env
}

func interpolated(value string) string {
	inner, ok := strings.CutPrefix(value, "${")
	if !ok || !strings.HasSuffix(inner, "}") {
		return value
	}
	inner = strings.TrimSuffix(inner, "}")
	for _, sep := range []string{":-", "-"} {
		if _, def, ok := strings.Cut(inner, sep); ok {
			return def
		}
	}
	return value
}
