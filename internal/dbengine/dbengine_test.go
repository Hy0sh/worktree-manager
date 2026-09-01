package dbengine

import (
	"strings"
	"testing"
)

func TestByNameResolvesEveryRegisteredEngineAndDefaultsToPostgres(t *testing.T) {
	for _, name := range Names() {
		if IsFileBased(name) {
			continue // no server commands to resolve
		}
		eng, err := ByName(name)
		if err != nil || eng.Name() != name {
			t.Fatalf("ByName(%q) = %v, %v", name, eng, err)
		}
	}
	eng, err := ByName("")
	if err != nil || eng.Name() != "postgres" {
		t.Fatalf("the empty name must resolve to postgres, got %v, %v", eng, err)
	}
	if _, err := ByName("oracle"); err == nil {
		t.Fatal("an unknown engine must be an error")
	}
}

func TestDetectMatchesCommonImages(t *testing.T) {
	cases := map[string]string{
		"postgres:16-alpine": "postgres",
		"postgis/postgis:15": "postgres",
		"bitnami/postgresql": "postgres",
	}
	for image, want := range cases {
		eng, ok := Detect(image)
		if !ok || eng.Name() != want {
			t.Fatalf("Detect(%q) = %v, %v, want %s", image, eng, ok, want)
		}
	}
	for _, image := range []string{"redis:7", "nginx", ""} {
		if _, ok := Detect(image); ok {
			t.Fatalf("Detect(%q) should not match", image)
		}
	}
}

// Detection decides which commands run against the container: an image that
// merely mentions an engine in its name (a proxy, a sidecar, a toolbox) must
// not be mistaken for the server itself. Detection is a default the stepper
// offers; missing one costs a question, matching wrong breaks the backup.
func TestDetectRejectsLookalikeImages(t *testing.T) {
	for _, image := range []string{
		"mycompany/mysql-proxy:1",
		"registry.example.com/platform/mysql-tools",
		"mycompany/mongo-backup-sidecar",
		"postgrest/postgrest:v12",
		"mariadb-operator/mariadb-operator",
	} {
		if eng, ok := Detect(image); ok {
			t.Fatalf("Detect(%q) = %s, a lookalike must not match", image, eng.Name())
		}
	}
	// The real servers keep matching, registries and tags stripped.
	for image, want := range map[string]string{
		"mysql/mysql-server:8.0":                       "mysql",
		"docker.io/library/mysql:8.4":                  "mysql",
		"mongodb/mongodb-community-server:7.0-ubi8":    "mongodb",
		"registry.example.com/mirror/bitnami/mariadb":  "mariadb",
		"postgres@sha256:0000000000000000000000000000": "postgres",
		// Known server variants: an enterprise or clustered build of an engine
		// is still that engine's server, unlike a proxy or a toolbox.
		"mongodb/mongodb-enterprise-server:7.0": "mongodb",
		"mongodb/mongodb-atlas-local:latest":    "mongodb",
		"percona/percona-server-mongodb:7.0":    "mongodb",
		"mysql/mysql-cluster:8.0":               "mysql",
		"percona:8.0":                           "mysql",
		"percona/percona-server:8.0":            "mysql",
		"bitnami/mariadb-galera:11":             "mariadb",
		"bitnami/postgresql-repmgr:16":          "postgres",
	} {
		eng, ok := Detect(image)
		if !ok || eng.Name() != want {
			t.Fatalf("Detect(%q) = %v, %v, want %s", image, eng, ok, want)
		}
	}
}

// sqlite has no server: it never resolves through ByName, callers branch on
// IsFileBased first, but it is a valid engine name everywhere names are
// checked or completed.
func TestFileBasedEngines(t *testing.T) {
	found := false
	for _, n := range Names() {
		if n == "sqlite" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Names() must list sqlite, got %v", Names())
	}
	if !IsFileBased("sqlite") || IsFileBased("") || IsFileBased("postgres") {
		t.Fatal("IsFileBased must single out sqlite")
	}
	for _, n := range append(Names(), "") {
		if !Valid(n) {
			t.Fatalf("Valid(%q) must hold", n)
		}
	}
	if Valid("oracle") {
		t.Fatal("an unknown engine is not valid")
	}
	if _, err := ByName("sqlite"); err == nil {
		t.Fatal("a file-based engine has no server commands to resolve")
	}
}

func TestTempDBNameSanitisesTheProjectName(t *testing.T) {
	if got := TempDBName("My-App"); got != "my_app_snapshot_tmp" {
		t.Fatalf("TempDBName = %q", got)
	}
}

func TestPostgresCommands(t *testing.T) {
	eng, _ := ByName("postgres")
	if got := strings.Join(eng.ReadyArgs("postgres"), " "); got != "pg_isready -h 127.0.0.1 -U postgres" {
		t.Fatalf("ready = %q", got)
	}
	if got := strings.Join(eng.DumpArgs("postgres", "x_tmp"), " "); got != "pg_dump -U postgres -Fc --no-owner --no-privileges -d x_tmp" {
		t.Fatalf("dump = %q", got)
	}
	if got := strings.Join(eng.DropTempDBArgs("postgres", "x_tmp"), " "); got != "psql -U postgres -c DROP DATABASE IF EXISTS x_tmp;" {
		t.Fatalf("drop = %q", got)
	}
	if eng.CreateTempDBArgs("postgres", "x_tmp") == nil {
		t.Fatal("postgres must create the temporary database")
	}
	script := eng.RestoreScript("myapp")
	for _, want := range []string{"/db-snapshot/myapp.dump", "pg_restore", "POSTGRES_DB"} {
		if !strings.Contains(script, want) {
			t.Fatalf("restore script must contain %q:\n%s", want, script)
		}
	}
}

// The postgres image runs a temporary server on the unix socket alone while it
// initialises a new data directory, then restarts for good. pg_isready over the
// socket answers during that window, and a migration launched then dies with
// "database system is shutting down". Only a TCP probe waits for the real one.
func TestPostgresReadyProbesOverTCP(t *testing.T) {
	eng, err := ByName("postgres")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(eng.ReadyArgs("postgres"), " ")
	if !strings.Contains(args, "-h 127.0.0.1") {
		t.Fatalf("the probe must go over TCP to skip the init-phase server, got %q", args)
	}
}
