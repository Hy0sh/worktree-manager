package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Hy0sh/worktree-manager/internal/compose"
	"github.com/Hy0sh/worktree-manager/internal/config"
	"github.com/Hy0sh/worktree-manager/internal/execx"
)

// frameworks are recognised by a file at the repository root, the one each
// framework generates there. A monorepo keeping its application in a
// subdirectory gets no suggestion, only the example.
var frameworks = []struct {
	marker, migrate, migrations string
}{
	{"manage.py", "python manage.py migrate", ""},
	{"bin/console", "php bin/console doctrine:migrations:migrate --no-interaction", ""},
	{"artisan", "php artisan migrate --force", ""},
	{"bin/rails", "bin/rails db:migrate", "db/migrate/*"},
	{"prisma/schema.prisma", "npx prisma migrate deploy", ""},
	{"alembic.ini", "alembic upgrade head", "alembic/versions/*"},
}

// suggestMigrations returns the migration command and pathspec of the framework
// the repository uses, "" for a pathspec the default already covers.
func suggestMigrations(dir string) (migrate, migrations string) {
	for _, f := range frameworks {
		if _, err := os.Stat(filepath.Join(dir, f.marker)); err == nil {
			return f.migrate, f.migrations
		}
	}
	return "", ""
}

// databaseVars are the names applications commonly read their database from.
var databaseVars = []string{"DATABASE_URL", "DB_NAME", "DB_DATABASE", "DATABASE_NAME", "POSTGRES_DB", "MYSQL_DATABASE"}

// suggestEnv turns the database variables the application service already sets
// in the compose file into their {{database}} form: a name is replaced whole,
// a URL keeps everything but the path.
func suggestEnv(dir, appService string) map[string]string {
	env := compose.ServiceEnvironment(dir, appService)
	suggested := map[string]string{}
	for _, key := range databaseVars {
		value, ok := env[key]
		if !ok {
			continue
		}
		if key != "DATABASE_URL" {
			suggested[key] = config.DatabasePlaceholder
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || strings.Contains(value, "${") {
			continue
		}
		// Rebuilt by hand: url.String would escape the braces of the placeholder.
		rebuilt, _, _ := strings.Cut(value, u.Host)
		rebuilt += u.Host + "/" + config.DatabasePlaceholder
		if u.RawQuery != "" {
			rebuilt += "?" + u.RawQuery
		}
		suggested[key] = rebuilt
	}
	if len(suggested) == 0 {
		return nil
	}
	return suggested
}

// otherDependencies is what the application service depends_on besides the
// database, the services start_dependencies would bring up.
func otherDependencies(dir, appService, dbService string) []string {
	all, err := compose.WithDependencies(dir, []string{appService})
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(all, func(s string) bool { return s == appService || s == dbService })
}

// localBranches lists the repository's branches when they are few enough to
// read as choices; past that they are noise, and nil.
func localBranches(runner execx.Runner, dir string) []string {
	if runner == nil {
		return nil
	}
	res, err := runner.Run(context.Background(), execx.Cmd{
		Name: "git",
		Args: []string{"-C", dir, "branch", "--format=%(refname:short)"},
	})
	if err != nil {
		return nil
	}
	branches := strings.Fields(res.Stdout)
	if len(branches) > 8 {
		return nil
	}
	return branches
}
