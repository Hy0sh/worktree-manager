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
