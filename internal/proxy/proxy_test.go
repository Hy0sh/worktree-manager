package proxy

import (
	"reflect"
	"testing"
)

// The shape of a worktree stack behind Traefik: a web entrypoint for the host
// names, and a second one taking any host, which must not steal the address.
func TestTraefikRoutesThroughTheRoutersEntrypoint(t *testing.T) {
	services := map[string]Service{
		"traefik": {
			Image: "traefik:v3.6",
			Command: []string{"--providers.docker.exposedbydefault=false",
				"--entryPoints.web.address=:80", "--entrypoints.mobile.address=:8000"},
			Ports: []Port{{Target: 80, Published: "25082"}, {Target: 8000, Published: "33002"}},
		},
		"api": {Labels: map[string]string{
			"traefik.enable":                          "true",
			"traefik.http.routers.api.rule":           "Host(`api.my-app.localhost`)",
			"traefik.http.routers.api.entrypoints":    "web",
			"traefik.http.routers.mobile.rule":        "PathPrefix(`/`)",
			"traefik.http.routers.mobile.entrypoints": "mobile",
		}},
		"front": {Labels: map[string]string{
			"traefik.enable":                  "true",
			"traefik.http.routers.front.rule": "Host(`a.localhost`) || Host(`b.localhost`)",
		}},
		"hidden": {Labels: map[string]string{
			"traefik.http.routers.hidden.rule": "Host(`hidden.localhost`)",
		}},
	}
	want := map[string][]string{
		"api":   {"http://api.my-app.localhost:25082"},
		"front": {"http://a.localhost:25082", "http://b.localhost:25082"},
	}
	if got, via := URLs(services); !reflect.DeepEqual(got, want) || via != "traefik" {
		t.Fatalf("got %v via %q, want %v via traefik", got, via, want)
	}
}

// Entrypoints set in a mounted traefik.yml leave the command empty: the
// conventional ports stand in, 443 for a tls router.
func TestTraefikFallsBackToConventionalPorts(t *testing.T) {
	services := map[string]Service{
		"proxy": {
			Image: "registry.example/mirror/traefik:v3@sha256:abc",
			Ports: []Port{{Target: 80, Published: "80"}, {Target: 443, Published: "28443"}},
		},
		"web": {Labels: map[string]string{
			"traefik.http.routers.web.rule": "Host(`web.localhost`)",
		}},
		"secure": {Labels: map[string]string{
			"traefik.http.routers.secure.rule": "Host(`secure.localhost`)",
			"traefik.http.routers.secure.tls":  "true",
		}},
	}
	want := map[string][]string{
		"web":    {"http://web.localhost"},
		"secure": {"https://secure.localhost:28443"},
	}
	if got, _ := URLs(services); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func ptr(s string) *string { return &s }

func TestNginxProxyReadsVirtualHost(t *testing.T) {
	services := map[string]Service{
		"proxy": {Image: "nginxproxy/nginx-proxy:1.6", Ports: []Port{{Target: 80, Published: "20080"}, {Target: 443, Published: "20443"}}},
		"front": {Environment: map[string]*string{"VIRTUAL_HOST": ptr("front.localhost, admin.localhost,~^api\\..*,*.example")}},
		"bare":  {Environment: map[string]*string{"VIRTUAL_HOST": nil}},
	}
	want := map[string][]string{"front": {"http://front.localhost:20080", "http://admin.localhost:20080"}}
	if got, via := URLs(services); !reflect.DeepEqual(got, want) || via != "nginx-proxy" {
		t.Fatalf("got %v via %q, want %v via nginx-proxy", got, via, want)
	}
}

// docker-gen writes the configuration, a stock nginx serves it on a port its
// HTTP_PORT moved, and only https is published on the other.
func TestNginxProxySplitWithDockerGen(t *testing.T) {
	services := map[string]Service{
		"gen":   {Image: "nginxproxy/docker-gen"},
		"nginx": {Image: "nginx:1", Environment: map[string]*string{"HTTP_PORT": ptr("8080")}, Ports: []Port{{Target: 8080, Published: "28080"}}},
		"front": {Environment: map[string]*string{"VIRTUAL_HOST": ptr("front.localhost")}},
	}
	want := map[string][]string{"front": {"http://front.localhost:28080"}}
	if got, _ := URLs(services); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCaddyReadsSiteLabels(t *testing.T) {
	services := map[string]Service{
		"caddy": {Image: "lucaslorentz/caddy-docker-proxy:2.9", Ports: []Port{
			{Target: 80, Published: "20080"}, {Target: 443, Published: "20443"}, {Target: 8443, Published: "28443"}}},
		"front": {Labels: map[string]string{
			"caddy":               "front.localhost",
			"caddy.reverse_proxy": "{{upstreams 3000}}",
			"caddy_1":             "http://plain.localhost, alt.localhost:8443",
		}},
	}
	want := map[string][]string{"front": {
		"https://front.localhost:20443", "http://plain.localhost:20080", "https://alt.localhost:28443"}}
	if got, via := URLs(services); !reflect.DeepEqual(got, want) || via != "caddy" {
		t.Fatalf("got %v via %q, want %v via caddy", got, via, want)
	}
}

func TestCaddyAutoHTTPSOff(t *testing.T) {
	services := map[string]Service{
		"caddy": {Image: "lucaslorentz/caddy-docker-proxy", Labels: map[string]string{"caddy.auto_https": "off"},
			Ports: []Port{{Target: 80, Published: "20080"}}},
		"front": {Labels: map[string]string{"caddy": "front.localhost"}},
	}
	want := map[string][]string{"front": {"http://front.localhost:20080"}}
	if got, _ := URLs(services); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNoKnownProxyNoURL(t *testing.T) {
	services := map[string]Service{
		"nginx": {Image: "nginx:1", Ports: []Port{{Target: 80, Published: "8080"}}},
		"web": {Labels: map[string]string{
			"traefik.http.routers.web.rule": "Host(`web.localhost`)",
		}},
	}
	if got, via := URLs(services); len(got) != 0 || via != "" {
		t.Fatalf("no Traefik runs, got %v via %q", got, via)
	}
}
