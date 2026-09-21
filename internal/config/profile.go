package config

import (
	"fmt"
	"sort"
	"strings"
)

// ServicesFor resolves a profile name to the compose services a stack starts.
// nil means every service, which is what a project without profiles, and a
// worktree naming none, has always done.
//
// The list is a floor and not an exact set: `compose up -d db backend` also
// brings up whatever backend declares in depends_on. Leaving a service out
// only keeps it down when nothing running depends on it.
func (p Project) ServicesFor(name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	services, ok := p.Profiles[name]
	if !ok {
		if len(p.Profiles) == 0 {
			return nil, fmt.Errorf("unknown profile %q: this project declares none "+
				"(add one to its `profiles` entry in config.json)", name)
		}
		return nil, fmt.Errorf("unknown profile %q, this project has %s",
			name, strings.Join(p.ProfileNames(), ", "))
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("profile %q names no service, which would start the whole stack "+
			"rather than nothing: remove it, or list what it should bring up", name)
	}
	// config.json is hand-edited, and these land on the docker command line.
	for _, service := range services {
		if err := ValidateIdentifier("compose service", service); err != nil {
			return nil, fmt.Errorf("profile %q: %w", name, err)
		}
	}
	return services, nil
}

// ProfileNames lists what a project offers, sorted so the completion and the
// error above agree from one call to the next.
func (p Project) ProfileNames() []string {
	names := make([]string, 0, len(p.Profiles))
	for name := range p.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
