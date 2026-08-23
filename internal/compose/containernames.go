package compose

import (
	"os"

	"gopkg.in/yaml.v3"
)

// PinnedContainerNames lists the services whose compose file fixes their
// container_name. wtm isolates a worktree stack by rebasing its ports, its
// volumes and its compose project name, but a container_name is none of
// those: docker refuses a second container carrying it, so the main stack and
// a worktree stack cannot both run. Nothing wtm generates can work around it,
// hence the warning at registration rather than a failure at the first start.
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
