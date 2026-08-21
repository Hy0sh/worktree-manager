package dbengine

import (
	"strings"
	"testing"
)

func TestMySQLFamilyDetection(t *testing.T) {
	cases := map[string]string{
		"mysql:8.4":            "mysql",
		"mysql":                "mysql",
		"mariadb:11":           "mariadb",
		"bitnami/mariadb:10.6": "mariadb",
	}
	for image, want := range cases {
		eng, ok := Detect(image)
		if !ok || eng.Name() != want {
			t.Fatalf("Detect(%q) = %v, %v, want %s", image, eng, ok, want)
		}
	}
}

// The root password expands inside the container: the argv wtm builds must
// carry the variable reference, never a value, and always through MYSQL_PWD
// so an empty password (MYSQL_ALLOW_EMPTY_PASSWORD) works too.
func TestMySQLCommandsExpandCredentialsInContainer(t *testing.T) {
	eng, _ := ByName("mysql")
	for _, args := range [][]string{
		eng.ReadyArgs("ignored"),
		eng.DropTempDBArgs("ignored", "x_tmp"),
		eng.CreateTempDBArgs("ignored", "x_tmp"),
		eng.DumpArgs("ignored", "x_tmp"),
	} {
		if len(args) != 3 || args[0] != "sh" || args[1] != "-c" {
			t.Fatalf("must run through sh -c for env expansion: %v", args)
		}
		if !strings.Contains(args[2], `MYSQL_PWD="$MYSQL_ROOT_PASSWORD"`) {
			t.Fatalf("credentials must come from the container env: %q", args[2])
		}
	}
	if s := eng.DumpArgs("ignored", "x_tmp")[2]; !strings.Contains(s, "mysqldump --single-transaction --routines --triggers x_tmp") {
		t.Fatalf("dump = %q", s)
	}
	if s := eng.DropTempDBArgs("ignored", "x_tmp")[2]; !strings.Contains(s, "DROP DATABASE IF EXISTS x_tmp") {
		t.Fatalf("drop = %q", s)
	}
	// A ping answers during the image's init phase, before root is usable;
	// only an authenticated query proves the final server is up.
	if s := eng.ReadyArgs("ignored")[2]; !strings.Contains(s, `mysql -uroot -e "SELECT 1"`) {
		t.Fatalf("ready = %q", s)
	}
}

func TestMariaDBUsesItsOwnBinariesAndPasswordVariable(t *testing.T) {
	eng, _ := ByName("mariadb")
	dump := eng.DumpArgs("ignored", "x_tmp")[2]
	if !strings.Contains(dump, "mariadb-dump") {
		t.Fatalf("recent mariadb images only ship mariadb-named binaries: %q", dump)
	}
	if !strings.Contains(dump, `MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"`) {
		t.Fatalf("mariadb reads MARIADB_ROOT_PASSWORD first: %q", dump)
	}
}

func TestMySQLFamilyRestoreScripts(t *testing.T) {
	mysqlEng, _ := ByName("mysql")
	script := mysqlEng.RestoreScript("myapp")
	for _, want := range []string{
		"/db-snapshot/myapp.dump",
		`mysql -uroot "${MYSQL_DATABASE:-mysql}"`,
		`MYSQL_PWD="$MYSQL_ROOT_PASSWORD"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("mysql restore script must contain %q:\n%s", want, script)
		}
	}
	mariaEng, _ := ByName("mariadb")
	script = mariaEng.RestoreScript("myapp")
	for _, want := range []string{
		`mariadb -uroot "${MARIADB_DATABASE:-${MYSQL_DATABASE:-mysql}}"`,
		`MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("mariadb restore script must contain %q:\n%s", want, script)
		}
	}
}
