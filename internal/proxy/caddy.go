package proxy

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// siteLabel is a caddy-docker-proxy site: `caddy`, or `caddy_0`, `caddy_1`
// for several sites on one service. Its value lists the site addresses.
var siteLabel = regexp.MustCompile(`^caddy(_\d+)?$`)

// caddy reads the site addresses of caddy-docker-proxy labels. Caddy serves
// a bare name over https, `.localhost` included, from its own local authority,
// unless the address says http:// or the global `auto_https off` is set.
func caddy(p Service, services map[string]Service, out map[string][]string) {
	ports := published(p)
	autoHTTPS := true
	for _, s := range services {
		for k, v := range lowerKeys(s.Labels) {
			if strings.HasSuffix(k, ".auto_https") && siteLabel.MatchString(strings.TrimSuffix(k, ".auto_https")) && v == "off" {
				autoHTTPS = false
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(services)) {
		labels := lowerKeys(services[name].Labels)
		var sites []string
		for k := range labels {
			if siteLabel.MatchString(k) {
				sites = append(sites, k)
			}
		}
		slices.Sort(sites)
		for _, site := range sites {
			for _, addr := range strings.FieldsFunc(labels[site], func(r rune) bool { return r == ',' || r == ' ' }) {
				host, target, tls, ok := siteAddress(addr, autoHTTPS)
				if !ok {
					continue
				}
				if port, ok := ports[target]; ok {
					out[name] = append(out[name], address(host, port, tls))
				}
			}
		}
	}
}

// siteAddress splits "http://app.localhost:8080/path" into its host, the
// container port Caddy listens on for it, and whether that port is https.
func siteAddress(addr string, autoHTTPS bool) (host string, target int, tls, ok bool) {
	scheme := ""
	if rest, found := strings.CutPrefix(addr, "http://"); found {
		scheme, addr = "http", rest
	} else if rest, found := strings.CutPrefix(addr, "https://"); found {
		scheme, addr = "https", rest
	}
	addr, _, _ = strings.Cut(addr, "/")
	host = addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
		if target, _ = strconv.Atoi(addr[i+1:]); target == 0 {
			return "", 0, false, false
		}
	}
	// ":8080" alone, or "*.example": no name to open.
	if host == "" || strings.Contains(host, "*") {
		return "", 0, false, false
	}
	switch {
	case target != 0:
		tls = scheme == "https" || (scheme == "" && autoHTTPS && target != 80)
	case scheme == "https" || (scheme == "" && autoHTTPS):
		target, tls = 443, true
	default:
		target = 80
	}
	return host, target, tls, true
}
