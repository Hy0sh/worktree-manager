package dbengine

type postgres struct{}

func (postgres) Name() string { return "postgres" }

func (postgres) DetectImage(repo string) bool {
	return baseIs(repo, "postgres", "postgresql", "postgis", "postgresql-repmgr")
}

// ReadyArgs probes over TCP on purpose: while the image initialises a new data
// directory it runs a temporary server on the unix socket alone, which
// pg_isready would answer for, then restarts; only the final server listens on TCP.
func (postgres) ReadyArgs(user string) []string {
	return []string{"pg_isready", "-h", "127.0.0.1", "-U", user}
}

func (postgres) DropTempDBArgs(user, db string) []string {
	return []string{"psql", "-U", user, "-c", "DROP DATABASE IF EXISTS " + db + ";"}
}

func (postgres) CreateTempDBArgs(user, db string) []string {
	return []string{"psql", "-U", user, "-c", "CREATE DATABASE " + db + ";"}
}

func (postgres) DumpArgs(user, db string) []string {
	return []string{"pg_dump", "-U", user, "-Fc", "--no-owner", "--no-privileges", "-d", db}
}

// ObjectCountArgs counts across every schema, not just public: a project can
// migrate into named schemas and still be perfectly populated.
func (postgres) ObjectCountArgs(user, db string) []string {
	return []string{"psql", "-U", user, "-d", db, "-t", "-A", "-c",
		"select count(*) from information_schema.tables where table_schema not in ('pg_catalog','information_schema')"}
}

// RestoreScript relies on docker-entrypoint-initdb.d only running on an empty
// data directory: a fresh worktree restores, an existing one is left alone, and
// nothing races the app's own migrate since healthy comes only after this.
func (postgres) RestoreScript(project string, extras []string) string {
	restore := `echo "wtm: restoring $dump into ${POSTGRES_DB:-postgres}"
pg_restore --no-owner --no-privileges --exit-on-error --single-transaction \
  -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" "$dump"`
	for _, db := range extras {
		restore += "\n" + restoreExtra(project, db)
	}
	return restoreScript(project, restore)
}

// restoreExtra creates the database only when missing: POSTGRES_DB may already
// name it. A dump older than the setting has no file for it, which is no error.
func restoreExtra(project, db string) string {
	return `extra="/db-snapshot/` + project + `.` + db + `.dump"
if [ -f "$extra" ]; then
  echo "wtm: restoring $extra into ` + db + `"
  psql -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" -tAc \
    "SELECT 1 FROM pg_database WHERE datname = '` + db + `'" | grep -q 1 \
    || createdb -U "${POSTGRES_USER:-postgres}" "` + db + `"
  pg_restore --no-owner --no-privileges --exit-on-error --single-transaction \
    -U "${POSTGRES_USER:-postgres}" -d "` + db + `" "$extra"
else
  echo "wtm: no dump at $extra, ` + db + ` is left as it is"
fi`
}
