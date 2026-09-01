package compose

import "sort"

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
