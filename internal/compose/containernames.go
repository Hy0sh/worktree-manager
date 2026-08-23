package compose

import (
	"os"

	"gopkg.in/yaml.v3"
)

// PinnedContainerNames lists the services whose compose file fixes their
// container_name. Ports, volumes and the compose project name are rebased, a
// container_name is not: docker refuses a second container carrying it, so the
// main stack and a worktree stack cannot both run.
func PinnedContainerNames(dir string) ([]string, error) {
	files, err := Files(dir)
	if err != nil {
		return nil, err
	}
	var (
		pinned []string
		seen   = map[string]bool{}
	)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		services := servicesNode(&doc)
		if services == nil || services.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(services.Content); i += 2 {
			name := services.Content[i].Value
			if seen[name] {
				continue
			}
			if mapValue(deref(services.Content[i+1]), "container_name") != nil {
				seen[name] = true
				pinned = append(pinned, name)
			}
		}
	}
	return pinned, nil
}
