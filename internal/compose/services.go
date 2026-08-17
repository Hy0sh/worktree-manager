package compose

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

var (
	servicesBlock = regexp.MustCompile(`^services:\s*$`)
	topLevelKey   = regexp.MustCompile(`^[A-Za-z0-9._-]+:`)
)

// Services lists the service names declared across the project's compose
// files, in declaration order, then alphabetically for what the overrides add.
// Unlike ServicePorts it tracks the `services:` block, because a name is only
// a service when it sits there: `volumes:` and `networks:` indent the same way.
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var (
		names  []string
		inside bool
	)
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case servicesBlock.MatchString(line):
			inside = true
		case topLevelKey.MatchString(line):
			inside = false
		case inside:
			if m := serviceHeader.FindStringSubmatch(line); m != nil {
				names = append(names, m[1])
			}
		}
	}
	return names, nil
}
