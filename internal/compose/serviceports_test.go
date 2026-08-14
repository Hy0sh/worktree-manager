package compose

import (
	"testing"
)

func TestServicePortsExtractsVariableAndContainerPort(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "compose.yaml", `services:
  db:
    image: postgres:16-alpine
    ports:
      - "${DB_PORT:-5432}:5432"
    environment:
      POSTGRES_USER: postgres
  backend:
    build: .
    ports:
      - "${BACKEND_PORT:-8000}:8000"
    depends_on:
      - db
  traefik:
    ports:
      - "9080:8080" # dashboard
      - 80:80
  api:
    ports:
      - "127.0.0.1:3001:3001"
volumes:
  pgdata:
`)
	ports, err := ServicePorts(path)
	if err != nil {
		t.Fatalf("ServicePorts: %v", err)
	}
	if len(ports) != 5 {
		t.Fatalf("got %d ports, want 5: %+v", len(ports), ports)
	}

	byService := map[string]ServicePort{}
	for _, p := range ports {
		if _, seen := byService[p.Service]; !seen {
			byService[p.Service] = p
		}
	}
	if p := byService["db"]; p.Var != "DB_PORT" || p.Host != "5432" || p.Container != "5432" {
		t.Fatalf("db = %+v", p)
	}
	if p := byService["backend"]; p.Var != "BACKEND_PORT" || p.Container != "8000" {
		t.Fatalf("backend = %+v", p)
	}
	// A trailing comment must not leak into the value.
	if p := byService["traefik"]; p.Var != "" || p.Host != "9080" || p.Container != "8080" {
		t.Fatalf("traefik = %+v", p)
	}
	// A host interface prefix is dropped, the last two numbers are the ports.
	if p := byService["api"]; p.Host != "3001" || p.Container != "3001" {
		t.Fatalf("api = %+v", p)
	}
	// `environment:` and `volumes:` must not be mistaken for port entries.
	for _, p := range ports {
		if p.Service == "" || p.Container == "" {
			t.Fatalf("bogus entry parsed: %+v", p)
		}
	}
}

// Only web ports deserve a clickable URL; a database shown as http:// would be
// actively misleading.
func TestIsWebOnlyForWebPorts(t *testing.T) {
	for _, container := range []string{"80", "3000", "8000", "8080"} {
		if !(ServicePort{Container: container}).IsWeb() {
			t.Fatalf("container port %s should be treated as web", container)
		}
	}
	for _, container := range []string{"5432", "6379", "9000", "1025"} {
		if (ServicePort{Container: container}).IsWeb() {
			t.Fatalf("container port %s should not be treated as web", container)
		}
	}
}

func TestPortLabelDisambiguatesMultiPortServices(t *testing.T) {
	cases := map[ServicePort]string{
		{Service: "mailhog", Var: "MAILHOG_WEB_PORT", Container: "8025"}:           "web",
		{Service: "mailhog", Var: "MAILHOG_SMTP_PORT", Container: "1025"}:          "smtp",
		{Service: "rustfs", Var: "RUSTFS_CONSOLE_PORT", Container: "9001"}:         "console",
		{Service: "kubectl", Var: "KUBECTL_MINIO_CONSOLE_PORT", Container: "9003"}: "minio-console",
		// Nothing left once the service name is stripped: fall back to the port.
		{Service: "backend", Var: "BACKEND_PORT", Container: "8000"}: "8000",
	}
	for in, want := range cases {
		if got := PortLabel(in); got != want {
			t.Fatalf("PortLabel(%s) = %q, want %q", in.Var, got, want)
		}
	}
}
