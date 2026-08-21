package dbengine

import (
	"strings"
	"testing"
)

func TestMongoDBDetection(t *testing.T) {
	for _, image := range []string{"mongo:7", "mongodb/mongodb-community-server", "bitnami/mongodb"} {
		eng, ok := Detect(image)
		if !ok || eng.Name() != "mongodb" {
			t.Fatalf("Detect(%q) = %v, %v", image, eng, ok)
		}
	}
}

// Auth flags are added inside the container, and only when the root user
// variables are set: a no-auth mongo needs none.
func TestMongoDBCommandsAuthenticateConditionally(t *testing.T) {
	eng, _ := ByName("mongodb")
	if eng.CreateTempDBArgs("ignored", "x_tmp") != nil {
		t.Fatal("mongo creates databases on first write, create must be skipped")
	}
	for _, args := range [][]string{
		eng.ReadyArgs("ignored"),
		eng.DropTempDBArgs("ignored", "x_tmp"),
		eng.DumpArgs("ignored", "x_tmp"),
	} {
		if len(args) != 3 || args[0] != "sh" || args[1] != "-c" {
			t.Fatalf("must run through sh -c: %v", args)
		}
		if !strings.Contains(args[2], `if [ -n "$MONGO_INITDB_ROOT_USERNAME" ]`) {
			t.Fatalf("auth must be conditional on the container env: %q", args[2])
		}
	}
	if s := eng.DumpArgs("ignored", "x_tmp")[2]; !strings.Contains(s, "mongodump --archive --db=x_tmp") {
		t.Fatalf("dump = %q", s)
	}
	if s := eng.DropTempDBArgs("ignored", "x_tmp")[2]; !strings.Contains(s, `db.getSiblingDB("x_tmp").dropDatabase()`) {
		t.Fatalf("drop = %q", s)
	}
}

// initdb.d runs against a temporary unauthed mongod, and the archive holds
// the throwaway database name, remapped to the one the application expects.
func TestMongoDBRestoreScriptRemapsTheDatabase(t *testing.T) {
	eng, _ := ByName("mongodb")
	script := eng.RestoreScript("myapp")
	for _, want := range []string{
		"/db-snapshot/myapp.dump",
		`--nsFrom='myapp_snapshot_tmp.*'`,
		`--nsTo="${MONGO_INITDB_DATABASE:-test}.*"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("restore script must contain %q:\n%s", want, script)
		}
	}
}
