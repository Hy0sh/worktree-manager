package compose

// BuiltServices lists the services built from the repository (`build:`), the
// ones running its code, in declaration order across the files.
func BuiltServices(dir string) []string {
	files, err := Files(dir)
	if err != nil {
		return nil
	}
	var built []string
	seen := map[string]bool{}
	for _, path := range files {
		services, err := servicesMapping(path)
		if err != nil || services == nil {
			continue
		}
		for i := 0; i+1 < len(services.Content); i += 2 {
			name := services.Content[i].Value
			if !seen[name] && mapValue(deref(services.Content[i+1]), "build") != nil {
				seen[name] = true
				built = append(built, name)
			}
		}
	}
	return built
}
