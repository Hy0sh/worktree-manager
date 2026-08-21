package config

import "testing"

func TestValidateIdentifierRejectsWhatWouldBeInjected(t *testing.T) {
	for _, name := range []string{"myapp", "my-app", "my_app", "app2", "db"} {
		if err := ValidateIdentifier("project name", name); err != nil {
			t.Fatalf("%q should be accepted: %v", name, err)
		}
	}
	// Each of these reaches either a generated shell script or generated YAML.
	for _, name := range []string{
		"", "My-App", "app name", `app"name`, "app$(id)", "app;rm -rf /",
		"app\nservices:", "-app", "../evil", "app/../x",
	} {
		if err := ValidateIdentifier("project name", name); err == nil {
			t.Fatalf("%q should be rejected", name)
		}
	}
}

// db_path is joined under each worktree and interpreted inside the app
// container: anything that could climb out of the project must be rejected.
func TestValidateRelativePathRejectsEscapes(t *testing.T) {
	for _, ok := range []string{"db.sqlite3", "var/data.db"} {
		if err := ValidateRelativePath("db_path", ok); err != nil {
			t.Fatalf("%q should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "../x.db", "/abs/x.db", "a/../../x.db"} {
		if err := ValidateRelativePath("db_path", bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}
