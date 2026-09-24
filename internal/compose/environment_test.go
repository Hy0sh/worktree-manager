package compose

import "testing"

func TestServiceEnvironmentReadsBothSyntaxesAndDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `services:
  db:
    environment:
      POSTGRES_USER: ${DB_USER:-app}
      POSTGRES_PASSWORD: ${DB_PASSWORD}
  api:
    environment:
      - DATABASE_URL=postgresql://app:app@db:5432/app
`)
	write(t, dir, "compose.override.yaml", "services:\n  db:\n    environment:\n      POSTGRES_DB: shop\n")

	db := ServiceEnvironment(dir, "db")
	if db["POSTGRES_USER"] != "app" || db["POSTGRES_PASSWORD"] != "${DB_PASSWORD}" || db["POSTGRES_DB"] != "shop" {
		t.Fatalf("db environment = %v", db)
	}
	if api := ServiceEnvironment(dir, "api"); api["DATABASE_URL"] != "postgresql://app:app@db:5432/app" {
		t.Fatalf("api environment = %v", api)
	}
}
