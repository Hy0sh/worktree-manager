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
	// A host interface prefix is preserved: rebasing must not widen a
	// loopback-only binding to every interface.
	if p := byService["api"]; p.HostIP != "127.0.0.1" || p.Host != "3001" || p.Container != "3001" {
		t.Fatalf("api = %+v", p)
	}
	// `environment:` and `volumes:` must not be mistaken for port entries.
	for _, p := range ports {
		if p.Service == "" || p.Container == "" {
			t.Fatalf("bogus entry parsed: %+v", p)
		}
	}
}

func TestServicePortsKeepsThePrefixOfAVariablePort(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "compose.yaml", `services:
  db:
    ports:
      - "127.0.0.1:${DB_PORT:-5432}:5432"
  cache:
    ports:
      - target: 6379
        published: "6379"
        host_ip: 127.0.0.1
`)
	ports, err := ServicePorts(path)
	if err != nil {
		t.Fatalf("ServicePorts: %v", err)
	}
	if len(ports) != 2 {
		t.Fatalf("got %d ports, want 2: %+v", len(ports), ports)
	}
	if p := ports[0]; p.HostIP != "127.0.0.1" || p.Var != "DB_PORT" || p.Host != "5432" {
		t.Fatalf("db = %+v", p)
	}
	if p := ports[1]; p.HostIP != "127.0.0.1" || p.Host != "6379" || p.Container != "6379" {
		t.Fatalf("cache = %+v", p)
	}
}

// The long ports syntax (target/published) used to be silently ignored, which
// left those services unisolated. An entry without a published side has no
// host port and still contributes nothing.
func TestServicePortsReadsTheLongSyntax(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "compose.yaml", `services:
  web:
    ports:
      - target: 3000
        published: "${WEB_PORT:-8080}"
        protocol: tcp
  metrics:
    ports:
      - target: 9090
        published: 9090
  internal:
    ports:
      - target: 6379
`)
	ports, err := ServicePorts(path)
	if err != nil {
		t.Fatalf("ServicePorts: %v", err)
	}
	if len(ports) != 2 {
		t.Fatalf("got %d ports, want 2: %+v", len(ports), ports)
	}
	if p := ports[0]; p.Service != "web" || p.Var != "WEB_PORT" || p.Host != "8080" || p.Container != "3000" {
		t.Fatalf("web = %+v", p)
	}
	if p := ports[1]; p.Service != "metrics" || p.Var != "" || p.Host != "9090" || p.Container != "9090" {
		t.Fatalf("metrics = %+v", p)
	}
}

// The old line-oriented parser assumed two-space indentation; real projects
// indent however their formatter likes.
func TestServicePortsSurvivesNonStandardIndentation(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "compose.yaml", `services:
    db:
        ports:
            - "${DB_PORT:-5432}:5432"
`)
	ports, err := ServicePorts(path)
	if err != nil {
		t.Fatalf("ServicePorts: %v", err)
	}
	if len(ports) != 1 || ports[0].Var != "DB_PORT" {
		t.Fatalf("ports = %+v", ports)
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
		{Service: "files", Var: "FILES_CONSOLE_PORT", Container: "9001"}:           "console",
		{Service: "tunnel", Var: "TUNNEL_STORAGE_CONSOLE_PORT", Container: "9003"}: "storage-console",
		// Nothing left once the service name is stripped: fall back to the port.
		{Service: "backend", Var: "BACKEND_PORT", Container: "8000"}: "8000",
	}
	for in, want := range cases {
		if got := PortLabel(in); got != want {
			t.Fatalf("PortLabel(%s) = %q, want %q", in.Var, got, want)
		}
	}
}

// A project that already remaps its ports on purpose must keep that mapping as
// the reference, otherwise rebasing from the base file would silently undo it.
func TestMergedServicePortsLetsTheOverrideWin(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `services:
  postgres:
    ports:
      - "5432:5432"
  api:
    ports:
      - "3000:3000"
  web:
    ports:
      - "8080:80"
`)
	write(t, dir, "compose.override.yaml", `services:
  postgres:
    ports: !override
      - "55432:5432"
  api:
    ports: !override
      - "18000:3000"
`)
	ports, err := MergedServicePorts(dir)
	if err != nil {
		t.Fatalf("MergedServicePorts: %v", err)
	}
	byService := map[string]ServicePort{}
	for _, p := range ports {
		byService[p.Service] = p
	}
	if got := byService["postgres"].Host; got != "55432" {
		t.Fatalf("postgres host port = %s, the override must win", got)
	}
	if got := byService["api"].Host; got != "18000" {
		t.Fatalf("api host port = %s, the override must win", got)
	}
	if got := byService["web"].Host; got != "8080" {
		t.Fatalf("web is absent from the override, it keeps %s", got)
	}
	if len(ports) != 3 {
		t.Fatalf("expected one port per service, got %+v", ports)
	}
}
