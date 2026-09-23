package compose

import (
	"sort"

	"gopkg.in/yaml.v3"
)

// Services lists the service names declared across the project's compose
// files, in declaration order, then alphabetically for what the overrides add.
func Services(dir string) ([]string, error) {
	files, err := Files(dir)
	if err != nil {
		return nil, err
	}
	var (
		names []string
		seen  = map[string]bool{}
	)
	for i, path := range files {
		found, err := servicesOf(path)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			sort.Strings(found)
		}
		for _, name := range found {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// WithDependencies adds to services what they declare in depends_on,
// transitively and across every compose file, which is what `compose up -d
// <services>` really starts. depends_on is either a list or a mapping.
func WithDependencies(dir string, services []string) ([]string, error) {
	files, err := Files(dir)
	if err != nil {
		return nil, err
	}
	deps := map[string][]string{}
	for _, path := range files {
		mapping, err := servicesMapping(path)
		if err != nil {
			return nil, err
		}
		if mapping == nil {
			continue
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			name := mapping.Content[i].Value
			on := mapValue(deref(mapping.Content[i+1]), "depends_on")
			if on == nil {
				continue
			}
			step := 1
			if on.Kind == yaml.MappingNode {
				step = 2
			}
			for j := 0; j < len(on.Content); j += step {
				deps[name] = append(deps[name], deref(on.Content[j]).Value)
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
		for _, dep := range deps[name] {
			visit(dep)
		}
	}
	for _, name := range services {
		visit(name)
	}
	return out, nil
}

func servicesOf(path string) ([]string, error) {
	services, err := servicesMapping(path)
	if err != nil || services == nil {
		return nil, err
	}
	var names []string
	for i := 0; i+1 < len(services.Content); i += 2 {
		names = append(names, services.Content[i].Value)
	}
	return names, nil
}
