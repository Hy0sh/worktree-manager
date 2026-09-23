package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ServicesFor resolves a profile to the services a stack starts, nil for all.
// The list is a floor: `compose up -d db backend` also starts backend's
// depends_on, so leaving a service out keeps it down only if nothing needs it.
func (p Project) ServicesFor(name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	services, ok := p.Profiles[name]
	if !ok {
		if len(p.Profiles) == 0 {
			return nil, fmt.Errorf("unknown profile %q: this project declares none "+
				"(`wtm project edit <project> --profile-set %s=db,backend`)", name, name)
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
	return slices.Sorted(maps.Keys(p.Profiles))
}
