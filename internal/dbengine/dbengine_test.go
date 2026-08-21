package dbengine

import (
	"strings"
	"testing"
)

func TestByNameResolvesEveryRegisteredEngineAndDefaultsToPostgres(t *testing.T) {
	for _, name := range Names() {
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

func TestTempDBNameSanitisesTheProjectName(t *testing.T) {
	if got := TempDBName("My-App"); got != "my_app_snapshot_tmp" {
		t.Fatalf("TempDBName = %q", got)
	}
}

func TestPostgresCommands(t *testing.T) {
	eng, _ := ByName("postgres")
	if got := strings.Join(eng.ReadyArgs("postgres"), " "); got != "pg_isready -U postgres" {
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
