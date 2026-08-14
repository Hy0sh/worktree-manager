package compose

import (
	"os"
	"regexp"
	"strings"
)

// ServicePort is one published port of a service, as declared in the base
// compose file.
type ServicePort struct {
	Service string
	// Var is the environment variable wtc overrides, empty when the port is
	// hardcoded and therefore not isolatable.
	Var       string
	Host      string // host port declared as the default
	Container string // container side, which is what tells a web port apart
}

// webPorts are the container-side ports worth turning into a clickable URL.
// Anything else (a database, a cache) is shown as a plain host:port.
var webPorts = map[string]bool{
	"80": true, "443": true, "3000": true, "3001": true, "4200": true,
	"5173": true, "8000": true, "8025": true, "8080": true, "9001": true,
}

// IsWeb reports whether this port is worth prefixing with http://.
func (s ServicePort) IsWeb() bool { return webPorts[s.Container] }

var (
	serviceHeader = regexp.MustCompile(`^  ([A-Za-z0-9._-]+):\s*$`)
	serviceKey    = regexp.MustCompile(`^    ([A-Za-z0-9._-]+):`)
	portsEntry    = regexp.MustCompile(`^\s+-\s+(.*)$`)

	paramEntry = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*):-(\d+)\}:(\d+)$`)
	plainEntry = regexp.MustCompile(`^(?:[\d.]+:)?(\d+):(\d+)$`)
)

// ServicePorts lists the published ports per service. It reads the base file
// only, mirroring wtc, which never merges override files.
func ServicePorts(path string) ([]ServicePort, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var (
		out     []ServicePort
		service string
		inPorts bool
	)
	for _, line := range strings.Split(string(data), "\n") {
		if m := serviceHeader.FindStringSubmatch(line); m != nil {
			service, inPorts = m[1], false
			continue
		}
		if m := serviceKey.FindStringSubmatch(line); m != nil {
			inPorts = m[1] == "ports"
			continue
		}
		if !inPorts || service == "" {
			continue
		}
		m := portsEntry.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if sp, ok := parseEntry(service, m[1]); ok {
			out = append(out, sp)
		}
	}
	return out, nil
}

func parseEntry(service, raw string) (ServicePort, bool) {
	// Drop trailing comments and quotes: `- "9080:8080" # Traefik dashboard`.
	if i := strings.Index(raw, "#"); i >= 0 {
		raw = raw[:i]
	}
	value := strings.Trim(strings.TrimSpace(raw), `"'`)

	if m := paramEntry.FindStringSubmatch(value); m != nil {
		return ServicePort{Service: service, Var: m[1], Host: m[2], Container: m[3]}, true
	}
	if m := plainEntry.FindStringSubmatch(value); m != nil {
		return ServicePort{Service: service, Host: m[1], Container: m[2]}, true
	}
	return ServicePort{}, false
}

// PortLabel distinguishes the several ports of one service, using the part of
// the variable name that is not the service name: MAILHOG_WEB_PORT on service
// mailhog becomes "web".
func PortLabel(s ServicePort) string {
	name := strings.TrimSuffix(strings.ToLower(s.Var), "_port")
	name = strings.TrimPrefix(name, strings.ToLower(strings.ReplaceAll(s.Service, "-", "_"))+"_")
	if name == "" || strings.EqualFold(name, s.Service) {
		return s.Container
	}
	return strings.ReplaceAll(name, "_", "-")
}
