package compose

// PinnedContainerNames lists the services whose compose file fixes their
// container_name: ports, volumes and the project name are rebased, that is
// not, and docker refuses a second container carrying it.
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
		services, err := servicesMapping(path)
		if err != nil {
			return nil, err
		}
		if services == nil {
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
