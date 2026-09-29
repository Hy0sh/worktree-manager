// Package proxy reads the host names a reverse proxy routes to the services of
// a compose project, and the host port each one is reached through. The input
// is `docker compose config --format json`; the proxy is told by its image.
package proxy

import (
	"maps"
	"path"
	"slices"
	"strings"
)

type Port struct {
	Target    int    `json:"target"`
	Published string `json:"published"`
}

type Service struct {
	Image   string            `json:"image"`
	Command []string          `json:"command"`
	Labels  map[string]string `json:"labels"`
	Ports   []Port            `json:"ports"`
	// A bare `- NAME` passes the host's value through: compose renders null.
	Environment map[string]*string `json:"environment"`
}

// reader fills out from the proxy service p, reading how the other services
// ask to be routed.
type reader func(p Service, services map[string]Service, out map[string][]string)

// readers are keyed by image base name, see imageBase.
var readers = map[string]struct {
	name string
	read reader
}{
	"traefik":            {"traefik", traefik},
	"nginx-proxy":        {"nginx-proxy", nginxProxy},
	"caddy-docker-proxy": {"caddy", caddy},
}

// Mentioned spares the docker call to a project whose compose files name no
// proxy wtm knows: a false positive costs one `compose config`, nothing more.
func Mentioned(composeText string) bool {
	text := strings.ToLower(composeText)
	return strings.Contains(text, "traefik") || strings.Contains(text, "virtual_host") ||
		strings.Contains(text, "caddy")
}

// URLs maps each service to the addresses a known proxy routes to it, and
// names that proxy. A service no route names is absent.
func URLs(services map[string]Service) (map[string][]string, string) {
	names := slices.Sorted(maps.Keys(services))
	// nginx-proxy split in two: docker-gen watches docker and writes the
	// configuration a stock nginx serves, so that nginx is the proxy.
	splitNginx := slices.ContainsFunc(names, func(n string) bool { return imageBase(services[n].Image) == "docker-gen" })
	out := map[string][]string{}
	for _, name := range names {
		base := imageBase(services[name].Image)
		if base == "nginx" && splitNginx {
			base = "nginx-proxy"
		}
		r, ok := readers[base]
		if !ok {
			continue
		}
		// ponytail: the first proxy only, a project running two is yet to be seen
		if r.read(services[name], services, out); len(out) > 0 {
			return out, r.name
		}
		break
	}
	return out, ""
}

// imageBase is the image's last path segment without tag or digest:
// "registry.example/mirror/traefik:v3@sha256:…" is "traefik".
func imageBase(image string) string {
	image = strings.ToLower(image)
	if i := strings.LastIndex(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}
	return path.Base(image)
}

// published maps each container port of p to the host port it is published on.
func published(p Service) map[int]string {
	out := map[int]string{}
	for _, port := range p.Ports {
		if port.Published != "" {
			out[port.Target] = port.Published
		}
	}
	return out
}

func env(s Service, name string) string {
	if v := s.Environment[name]; v != nil {
		return *v
	}
	return ""
}

func lowerKeys(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[strings.ToLower(k)] = v
	}
	return out
}

func address(host, port string, tls bool) string {
	scheme, standard := "http", "80"
	if tls {
		scheme, standard = "https", "443"
	}
	if port == standard {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}
