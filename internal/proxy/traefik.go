package proxy

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

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

// traefik reads each router's Host() rules, on the host port the proxy
// publishes for the router's entrypoint.
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
	ports := published(p)

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
			port, ok := routerPort(labels[prefix+"entrypoints"], entrypoints, ports, &tls)
			if !ok {
				continue
			}
			for _, host := range hosts {
				out[name] = append(out[name], address(host, port, tls))
			}
		}
	}
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
