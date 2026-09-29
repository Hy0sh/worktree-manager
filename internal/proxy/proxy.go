// Package proxy reads the host names a reverse proxy routes to the services of
// a compose project, and the host port each one is reached through. Only
// Traefik is known so far; the input is `docker compose config --format json`.
package proxy

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
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
}

// Mentioned spares the docker call to a project whose compose files name no
// proxy wtm knows: a false positive costs one `compose config`, nothing more.
func Mentioned(composeText string) bool {
	return strings.Contains(strings.ToLower(composeText), "traefik")
}

// URLs maps each service to the addresses a known proxy routes to it, in
// router order, and names that proxy. A service no router names is absent.
func URLs(services map[string]Service) (map[string][]string, string) {
	out := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(services)) {
		if s := services[name]; imageBase(s.Image) == "traefik" {
			// ponytail: first Traefik only, a project running two is yet to be seen
			if traefik(s, services, out); len(out) > 0 {
				return out, "traefik"
			}
			break
		}
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

var (
	hostRule   = regexp.MustCompile(`Host\(([^)]*)\)`)
	quotedName = regexp.MustCompile("[`\"]([^`\"]+)[`\"]")
)

// entrypoint is one `--entrypoints.<name>.address=:<port>` of the Traefik
// command, and whether `--entrypoints.<name>.http.tls` makes it https.
type entrypoint struct {
	port int
	tls  bool
}

func traefik(p Service, services map[string]Service, out map[string][]string) {
	entrypoints := map[string]*entrypoint{}
	exposedByDefault := true
	for _, arg := range p.Command {
		key, value, _ := strings.Cut(strings.TrimLeft(strings.ToLower(arg), "-"), "=")
		if key == "providers.docker.exposedbydefault" {
			exposedByDefault = value != "false"
			continue
		}
		name, field, ok := strings.Cut(strings.TrimPrefix(key, "entrypoints."), ".")
		if !ok || !strings.HasPrefix(key, "entrypoints.") {
			continue
		}
		ep := entrypoints[name]
		if ep == nil {
			ep = &entrypoint{}
			entrypoints[name] = ep
		}
		switch {
		case field == "address":
			// ":80", "0.0.0.0:80" or ":80/tcp"
			addr, _, _ := strings.Cut(value, "/")
			ep.port, _ = strconv.Atoi(addr[strings.LastIndex(addr, ":")+1:])
		case strings.HasPrefix(field, "http.tls") && value != "false":
			ep.tls = true
		}
	}
	published := map[int]string{}
	for _, port := range p.Ports {
		if port.Published != "" {
			published[port.Target] = port.Published
		}
	}

	for _, name := range slices.Sorted(maps.Keys(services)) {
		labels := lowerKeys(services[name].Labels)
		if enable, set := labels["traefik.enable"]; enable == "false" || (!exposedByDefault && !set) {
			continue
		}
		for _, router := range routers(labels) {
			prefix := "traefik.http.routers." + router + "."
			hosts := hostsOf(labels[prefix+"rule"])
			if len(hosts) == 0 {
				continue // PathPrefix or HostRegexp: no address to print
			}
			tls := labels[prefix+"tls"] == "true" || labels[prefix+"tls.certresolver"] != ""
			port, ok := routerPort(labels[prefix+"entrypoints"], entrypoints, published, &tls)
			if !ok {
				continue
			}
			for _, host := range hosts {
				out[name] = append(out[name], address(host, port, tls))
			}
		}
	}
}

func lowerKeys(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[strings.ToLower(k)] = v
	}
	return out
}

func routers(labels map[string]string) []string {
	var out []string
	for k := range labels {
		if rest, ok := strings.CutPrefix(k, "traefik.http.routers."); ok {
			if router, field, ok := strings.Cut(rest, "."); ok && field == "rule" {
				out = append(out, router)
			}
		}
	}
	slices.Sort(out)
	return out
}

func hostsOf(rule string) []string {
	var out []string
	for _, m := range hostRule.FindAllStringSubmatch(rule, -1) {
		for _, q := range quotedName.FindAllStringSubmatch(m[1], -1) {
			out = append(out, q[1])
		}
	}
	return out
}

// routerPort is the host port of the first entrypoint the router listens on
// that the proxy publishes. A router naming none listens on all of them. An
// entrypoint defined in a mounted traefik.yml is invisible here, so without any
// from the command the conventional 80, or 443 for tls, stands in.
func routerPort(named string, entrypoints map[string]*entrypoint, published map[int]string, tls *bool) (string, bool) {
	if len(entrypoints) == 0 {
		target := 80
		if *tls {
			target = 443
		}
		port, ok := published[target]
		return port, ok
	}
	// Any of them works; the one on the web port is the address people expect.
	names := slices.SortedFunc(maps.Keys(entrypoints), func(a, b string) int {
		web := func(p int) bool { return p == 80 || p == 443 }
		wa, wb := web(entrypoints[a].port), web(entrypoints[b].port)
		if wa != wb {
			if wa {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	if named != "" {
		names = strings.Split(strings.ToLower(named), ",")
	}
	for _, n := range names {
		ep := entrypoints[strings.TrimSpace(n)]
		if ep == nil {
			continue
		}
		if port, ok := published[ep.port]; ok {
			*tls = *tls || ep.tls
			return port, true
		}
	}
	return "", false
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
