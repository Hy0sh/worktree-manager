# worktree-manager

Go CLI that creates a ready-to-use git worktree and manages its full
lifecycle: creation, stack startup, stop, removal, plus a pre-migrated
Postgres backup that makes bootstrapping a database fast.

Port allocation and docker compose aren't reimplemented: they're delegated
to [`wtc`](https://github.com/mostafasudo/worktree-compose)
(worktree-compose), invoked as a subprocess. `wtm` remains the sole
user-facing entry point.

## Installation

```sh
# global install into a directory already on PATH
GOBIN=$HOME/.local/bin go install ./cmd/wtm
```

Without `GOBIN`, `go install` drops the binary in `$(go env GOPATH)/bin`
(`~/go/bin`), which isn't always on PATH. To build without installing:
`go build -o wtm ./cmd/wtm`.

After changing the code, rerun the same command to refresh the installed
binary.

## Prerequisites

- `git`, `docker` / `docker compose` on PATH.
- On the target project's side: Node >= 18 and `worktree-compose` as a
  devDependency (`node_modules/.bin/wtc`). Without it, only
  `--no-start` creation works.
- Host ports in the project's `compose.yaml` parameterized as `${VAR:-default}`,
  otherwise wtc can't isolate them.
- For `dump: true`: the project must mount `./.db-snapshot` and a restore
  script under `docker-entrypoint-initdb.d`, and that script must target
  `<project>.dump`.

## Usage

```sh
# register a project (once)
wtm project create my-project --dir ~/dev/projects/my-project --base develop
wtm project list

# create a worktree + start its stack
wtm my-app feat/my-branch            # base = the project's own
wtm feat/my-branch                          # project = current repo
wtm feat/my-branch main                     # explicit base
wtm feat/my-branch --no-start               # without starting the stack

# lifecycle
wtm stop feat/my-branch
wtm remove feat/my-branch                   # local branch kept
wtm remove feat/my-branch --force           # despite modified tracked files

# Postgres backup
wtm backup refresh my-app            # starts db+backend if needed
wtm backup list
wtm backup remove my-app
```

If the first argument is a registered project, it's treated as such;
otherwise it's a branch of the current directory's project. A branch
named `stop`, `remove`, `project`, or `backup` would be interpreted as
a subcommand: in that case, go through git directly.

## Configuring the backup

Nothing is hardcoded: service names, database user, migration command, and
how the database is designated are specific to each project. Real
examples, all expressible in configuration:

```sh
# Django + poetry (dependencies installed at container startup)
wtm project create my-app --dir ~/dev/projects/my-app \
  --dump --git-container --app-service backend \
  --deps 'poetry install --no-root --with dev' \
  --migrate 'python manage.py migrate' \
  --env 'DB_NAME={{database}}'

# Prisma + bun, differently named database, non-postgres user
wtm project create webshop --dir ~/dev/projects/webshop \
  --dump --db-user appuser --app-service api \
  --deps 'bun install --frozen-lockfile' \
  --migrate 'bunx prisma migrate deploy' \
  --env 'DATABASE_URL=postgresql://appuser:secret@db:5432/{{database}}'

# MikroORM + pnpm, database service named "postgres"
wtm project create platform --dir ~/dev/projects/platform \
  --dump --db-service postgres --app-service api \
  --deps 'pnpm install' \
  --migrate 'pnpm --filter @acme/api migration:up' \
  --env 'DATABASE_URL=postgresql://postgres:postgres@postgres:5432/{{database}}'
```

`{{database}}` gets replaced by the temporary database's name. `--db-service`
defaults to `db` and `--db-user` defaults to `postgres`. These settings can
be reviewed and tweaked directly in `config.json`.

Two things worth flagging. `--git-container` is only useful for projects
whose compose bind-mounts the git-dir into a container; left off, it
creates nothing. And wtc can only isolate ports if the compose file
expresses them as `${VAR:-default}`: a project with hardcoded ports will
need adapting before automatic worktree startup makes sense.

## Diagnostics

```sh
wtm doctor
```

Shows the config and backups paths, the memory actually used by the Docker
VM, and above all **which wtc binary will run** for each project. That last
piece of information stops being obvious once wtc can come from three
places: an explicit `wtc_bin` path, the project's devDependency, or a
global install. Priority is exactly in that order. The version shown is
read from the package's `package.json`, since `wtc --version` reports
`0.1.0` even on 0.2.0.

Before starting a stack, the tool compares the Docker VM's measured usage
against its capacity and warns (without blocking) if one more stack risks
saturating it. The estimate is based on the average observed consumption of
running stacks, never on declared `mem_limit`s: one stack here declares
over 13 GB of cumulative caps while actually running in 2 GB. Ephemeral
`compose run` containers are excluded from that average.

## Storage

| What | Where |
|---|---|
| Project registry | `~/.config/wtm/config.json` |
| Dumps + metadata | `~/.config/wtm/backups/<project>/<project>.dump` |

A single dump per project, never duplicated: each worktree accesses it
through a `.db-snapshot` directory symlink. `WTM_CONFIG_DIR` and
`WTM_BACKUPS_DIR` let you relocate these paths.

## Tests

```sh
go test ./...        # unit tests, no dependency on git/docker/node
go vet ./...
gofmt -l .
```

Sequences of external commands are verified through `internal/execx` (an
injectable runner). What isn't covered automatically, to check by hand on
a real project (my-app):

1. `wtm <project> <branch> --no-start` -> the worktree contains the
   `*.env` files, the `.git-container` and `.db-snapshot` symlinks.
2. Without `--no-start` -> `wtc start` runs and the allocated ports are
   displayed.
3. `wtm backup refresh <project>` with the stack stopped -> it starts on
   its own, the dump shows up in `backup list`.
4. `wtm stop` then `remove` -> stack stopped, worktree removed, local
   branch still present.
