package proxy

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// nginxProxy reads each service's VIRTUAL_HOST, a comma-separated list. The proxy
// listens on 80 and 443 unless HTTP_PORT or HTTPS_PORT move them. The http port
// wins when both are published: a mounted certificate is invisible in compose.
func nginxProxy(p Service, services map[string]Service, out map[string][]string) {
	ports := published(p)
	target := func(name string, standard int) int {
		if n, err := strconv.Atoi(env(p, name)); err == nil {
			return n
		}
		return standard
	}
	port, ok := ports[target("HTTP_PORT", 80)]
	tls := false
	if !ok {
		if port, ok = ports[target("HTTPS_PORT", 443)]; !ok {
			return
		}
		tls = true
	}
	for _, name := range slices.Sorted(maps.Keys(services)) {
		for _, host := range strings.Split(env(services[name], "VIRTUAL_HOST"), ",") {
			host = strings.TrimSpace(host)
			// "~^api\..*" is a regular expression, "*.example" a wildcard:
			// neither is an address to open.
			if host == "" || strings.HasPrefix(host, "~") || strings.Contains(host, "*") {
				continue
			}
			out[name] = append(out[name], address(host, port, tls))
		}
	}
}
