package compose

import (
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServicePort struct {
	Service string
	// Var is the environment variable wtc overrides, empty when the port is
	// hardcoded and therefore not isolatable.
	Var       string
	HostIP    string // interface the project binds to (IPv4), empty when unset
	Host      string // host port declared as the default
	Container string // container side, which is what tells a web port apart
}

// webPorts are the container-side ports worth turning into a clickable URL.
// Anything else (a database, a cache) is shown as a plain host:port.
var webPorts = map[string]bool{
	"80": true, "443": true, "3000": true, "3001": true, "4200": true,
	"5173": true, "8000": true, "8025": true, "8080": true, "9001": true,
}

func (s ServicePort) IsWeb() bool { return webPorts[s.Container] }

var (
	paramShort = regexp.MustCompile(`^(?:([\d.]+):)?\$\{([A-Za-z_][A-Za-z0-9_]*):-(\d+)\}:(\d+)$`)
	plainShort = regexp.MustCompile(`^(?:([\d.]+):)?(\d+):(\d+)$`)
	paramValue = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*):-(\d+)\}$`)
	plainValue = regexp.MustCompile(`^\d+$`)
	// hostIP is what the short-form regexes accept; the long form's host_ip
	// is held to the same charset so PortsOverride can emit it verbatim.
	hostIP = regexp.MustCompile(`^[\d.]+$`)
)

// ServicePorts lists the published ports per service, from that one file only:
// merging across files is MergedServicePorts' job.
func ServicePorts(path string) ([]ServicePort, error) {
	services, err := servicesMapping(path)
	if err != nil || services == nil {
		return nil, err
	}
	var out []ServicePort
	for i := 0; i+1 < len(services.Content); i += 2 {
		name := services.Content[i].Value
		ports := mapValue(deref(services.Content[i+1]), "ports")
		if ports == nil || ports.Kind != yaml.SequenceNode {
			continue
		}
		for _, entry := range ports.Content {
			if sp, ok := parsePortNode(name, deref(entry)); ok {
				out = append(out, sp)
			}
		}
	}
	return out, nil
}

// servicesMapping is one file's `services:` block, nil when it declares none.
// The file is walked as a YAML tree rather than decoded into structs, so
// compose's own tags (!override, !reset) and anchors pass through.
func servicesMapping(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	root := deref(&doc)
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = deref(root.Content[0])
	}
	services := mapValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil
	}
	return services, nil
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return deref(n.Content[i+1])
		}
	}
	return nil
}

// deref follows a YAML alias to its anchor, so `ports: *shared` still reads.
func deref(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

// parsePortNode understands both compose syntaxes: the short scalar form
// ("HOST:CONTAINER", with an optional interface prefix or ${VAR:-default}
// host), and the long mapping form (target/published).
func parsePortNode(service string, n *yaml.Node) (ServicePort, bool) {
	switch n.Kind {
	case yaml.ScalarNode:
		value := strings.TrimSpace(n.Value)
		if m := paramShort.FindStringSubmatch(value); m != nil {
			return ServicePort{Service: service, HostIP: m[1], Var: m[2], Host: m[3], Container: m[4]}, true
		}
		if m := plainShort.FindStringSubmatch(value); m != nil {
			return ServicePort{Service: service, HostIP: m[1], Host: m[2], Container: m[3]}, true
		}
	case yaml.MappingNode:
		target := mapValue(n, "target")
		published := mapValue(n, "published")
		if target == nil || published == nil {
			// Without a published side there is no host port to isolate.
			return ServicePort{}, false
		}
		sp := ServicePort{Service: service, Container: target.Value}
		if ip := mapValue(n, "host_ip"); ip != nil && hostIP.MatchString(ip.Value) {
			sp.HostIP = ip.Value
		}
		if m := paramValue.FindStringSubmatch(published.Value); m != nil {
			sp.Var, sp.Host = m[1], m[2]
			return sp, true
		}
		if plainValue.MatchString(published.Value) {
			sp.Host = published.Value
			return sp, true
		}
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

// MergedServicePorts reads the base file then lets the project's own override
// win, service by service: a project already remapping a port on purpose must
// keep that mapping, or rebasing from the base file would silently undo it.
func MergedServicePorts(dir string) ([]ServicePort, error) {
	files, err := Files(dir)
	if err != nil {
		return nil, err
	}
	var merged []ServicePort
	overridden := map[string]bool{}
	for i := len(files) - 1; i >= 0; i-- {
		ports, err := ServicePorts(files[i])
		if err != nil {
			return nil, err
		}
		// Compose replaces the whole `ports` list of a service when an override
		// declares one, so the first file that mentions a service wins here.
		seenHere := map[string]bool{}
		for _, sp := range ports {
			if overridden[sp.Service] && !seenHere[sp.Service] {
				continue
			}
			seenHere[sp.Service] = true
			merged = append(merged, sp)
		}
		for service := range seenHere {
			overridden[service] = true
		}
	}
	return merged, nil
}
