package compose

import (
	"path/filepath"
	"strings"
)

// GitDirMount returns the first volume source that is the repository's .git,
// or the .git-container link standing in for it, e.g. "./.git" out of
// "./.git:/app/.git:ro". Both volume syntaxes are read, short and long.
func GitDirMount(dir string) (string, bool) {
	files, err := Files(dir)
	if err != nil {
		return "", false
	}
	for _, path := range files {
		services, err := servicesMapping(path)
		if err != nil || services == nil {
			continue
		}
		for i := 1; i < len(services.Content); i += 2 {
			volumes := mapValue(deref(services.Content[i]), "volumes")
			if volumes == nil {
				continue
			}
			for _, v := range volumes.Content {
				v = deref(v)
				source := v.Value
				if s := mapValue(v, "source"); s != nil {
					source = s.Value
				}
				source, _, _ = strings.Cut(source, ":")
				if base := filepath.Base(source); base == ".git" || base == ".git-container" {
					return source, true
				}
			}
		}
	}
	return "", false
}
