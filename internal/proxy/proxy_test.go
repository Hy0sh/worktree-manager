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
