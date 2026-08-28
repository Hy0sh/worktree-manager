# worktree-manager

[![Go Reference](https://pkg.go.dev/badge/github.com/Hy0sh/worktree-manager.svg)](https://pkg.go.dev/github.com/Hy0sh/worktree-manager)

Go CLI that creates a ready-to-use git worktree and manages its full
lifecycle: creation, stack startup, stop, removal, plus a pre-migrated
database backup (Postgres, MySQL, MariaDB, MongoDB or SQLite) that makes
bootstrapping a database fast.

It has no runtime dependency beyond `git` and `docker`: port allocation,
compose project naming and the stack lifecycle are all handled directly.
Port assignments follow the same formula
[worktree-compose](https://github.com/mostafasudo/worktree-compose) used, and
the same `.env` markers, so a worktree created with that tool keeps the ports
it already had.

## Requirements

| | Why | When |
|---|---|---|
| Go >= 1.24 | to install the binary | install only |
| `git` >= 2.31 | worktrees, and `--path-format` to resolve the repository from inside one | always |
| `docker` with Compose v2 | starting a worktree's stack | projects that have one |

Nothing else, on your machine or in the target project: no Node, no plugin, no
file to add to the repository you point it at. A project without a compose file
works too, there is simply no stack to start.

Compose `!override` is used to rebase ports declared as literals, which needs
Compose v2.24 or later.

## Installation

No clone required:

```sh
go install github.com/Hy0sh/worktree-manager/cmd/wtm@latest
```

That puts the binary in `$(go env GOPATH)/bin`, usually `~/go/bin`, which is
not always on your PATH. Either add it, or send the binary somewhere already on
it:

```sh
GOBIN=$HOME/.local/bin go install github.com/Hy0sh/worktree-manager/cmd/wtm@latest
```

Check it with `wtm --version`, which reports the tag it was installed from, or
the commit when built from a working copy.

Without Go, take the binary from the
[latest release](https://github.com/Hy0sh/worktree-manager/releases/latest),
which carries one per platform (darwin and linux, arm64 and amd64) plus a
`SHA256SUMS` to check them against:

```sh
tar -xzf wtm_v0.9.1_darwin_arm64.tar.gz wtm
mv wtm ~/.local/bin/
```

Those are built by `go install` from the tag itself, so they report their
version exactly like an installed one.

From a clone, for hacking on it: `go install ./cmd/wtm`, or `go build -o wtm
./cmd/wtm` to build without installing. Rerun it after each change to refresh
the installed binary.

## Shell completion

Completion scripts are generated for bash, zsh, fish and powershell. Each
shell has its own conventions; `wtm completion <shell> --help` prints the
exact steps for yours. The usual ones:

```sh
# zsh: any directory on your fpath works
mkdir -p ~/.zsh/completions
wtm completion zsh > ~/.zsh/completions/_wtm
# then, in ~/.zshrc, before compinit runs:
#   fpath=(~/.zsh/completions $fpath)
#   autoload -Uz compinit && compinit

# bash: requires the bash-completion package
wtm completion bash > ~/.local/share/bash-completion/completions/wtm

# fish
wtm completion fish > ~/.config/fish/completions/wtm.fish
```

With oh-my-zsh, `~/.oh-my-zsh/completions` is already on the fpath, so
dropping `_wtm` there is enough and no `.zshrc` change is needed.

Open a new shell afterwards. The script queries the binary at completion
time, so it only needs regenerating when you upgrade to a version that adds
commands. Arguments complete too, not just subcommands and flags:

```
wtm <TAB>                    # registered project names
wtm stop <TAB>               # projects, plus branches that have a worktree
wtm stop myapp <TAB>         # that project's worktree branches
wtm backup refresh <TAB>     # registered project names
```

## Usage

```sh
# register a project (once)
wtm project create                                        # asks, the name included
wtm project create my-app                                 # asks for the rest
wtm project create my-app --dir ~/dev/projects/my-app --base develop
wtm project list

# change a registered project, its backup settings included
wtm project edit my-app                                   # asks, step by step
wtm project edit my-app --dump --app-service backend

# create a worktree + start its stack
wtm create my-app feat/my-branch            # base = the project's own
wtm create feat/my-branch                   # project = current repo
wtm create feat/my-branch main              # explicit base
wtm create feat/my-branch --no-start        # without starting the stack
wtm create feat/my-branch --no-post-create  # stack started, post_create skipped
wtm create feat/my-branch --run claude       # a command on your machine, once ready
wtm create feat/my-branch --exec 'manage.py load_fixture demo'   # in the container
wtm create feat/my-branch --ignore-memory    # never ask, however tight the RAM

# lifecycle
wtm list                                    # worktrees wtm created for this project
wtm start feat/my-branch                    # bring a stopped stack back up
wtm stop feat/my-branch
wtm stop --all                              # every worktree of the project
wtm remove feat/my-branch                   # stack, volumes and built images go, branch kept
wtm remove feat/my-branch --force           # despite modified tracked files, or a lock
wtm remove --all                            # lists them, asks once, -y answers for a script

# inside the worktree's containers
wtm exec feat/my-branch -- python manage.py seed_data
wtm exec feat/my-branch -- bash
wtm exec feat/my-branch --service db -- psql -U postgres

# on your machine, from the worktree directory, with COMPOSE_PROJECT_NAME
# and COMPOSE_FILE pointing at this worktree's stack
wtm run feat/my-branch -- claude
wtm run feat/my-branch -- git status
wtm run feat/my-branch -- scripts/some-compose-script.sh
cd $(wtm path feat/my-branch)

# database backup
wtm backup refresh my-app            # starts db+backend if needed
wtm backup list
wtm backup remove my-app
```

If the first argument is a registered project, it's treated as such;
otherwise it's a branch of the current directory's project.

`create` picks up an existing branch rather than shadowing it. A local
branch is checked out as-is; a branch that only exists on a remote is
checked out tracking it, fetching first in case it was pushed since your
last fetch. `<base>` is ignored in both cases, and only used for a branch
that exists nowhere yet. Should two remotes carry the same branch name, wtm
refuses to guess, as git does.

`exec` and `run` are deliberately distinct: `exec` enters the stack's
container, `run` stays on your machine with the worktree as working
directory, which is what an editor or a coding agent needs. `wtm list`
reports whether each stack is up, and falls back to `-` rather than hanging
when docker is slow or down.

Creation deliberately sits behind the `create` verb. It used to be the bare
form, `wtm <branch>`, until a mistyped `wtm list` created a branch called
`list` along with its worktree: any unknown word silently mutated the
repository. Now only a real subcommand runs, and anything else is rejected.

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
defaults to `db` and `--db-user` defaults to `postgres`.

## Seeding a fresh worktree

The dump carries what the migrations create and never seed data, so a new
worktree comes up migrated and empty. `--post-create` is the command that makes
it usable, played in the application service once the database answers and that
service reports itself healthy:

```sh
wtm project edit my-app --post-create 'python manage.py seed_data && python manage.py create_dev_users'
```

wtm waits, because a container docker calls started is not one that can run a
management command: a stack installing its dependencies from its `command:` (a
`poetry install` or an `npm ci` at boot) answers minutes later. A declared
`healthcheck:` is what it waits on. A service without one is waited on by its
first published port, read inside the container, since the host side of that
port accepts a connection while nothing listens behind it yet. A service with
neither, a queue worker for instance, leaves nothing to wait for: the command
runs as soon as the database answers, and a warning says so.

Both waits are bounded, and a project can say by how much:

```sh
wtm project edit my-app --ready-timeout 2m --ready-interval 10s
```

`ready_timeout` is how long each service gets, `ready_interval` how often it is
asked. Left unset, a database gets a minute and an application ten, asked every
second, and a wait that holds repeats itself every thirty seconds with the time
elapsed. A timeout is not a failed creation: the worktree stands, and the
warning names the `wtm exec` line that replays the command.

Do not point it at a script that resets the database: dropping the schema to
migrate it again throws away the restored dump and pays for the migrations wtm
exists to skip. Seed only. A failing command is a warning and not a failed
creation, and the warning names the `wtm exec` line that replays it.

Postgres, MySQL, MariaDB, MongoDB and SQLite are supported. The engine is
read from the database service's image in the compose file and offered as the
default at registration, then recorded as `db_engine` (`--db-engine`
overrides it). Database credentials are never stored by wtm: every engine
command reads them from the container's own environment (`POSTGRES_*`,
`MYSQL_ROOT_PASSWORD`, `MONGO_INITDB_*`).

SQLite has no database service, so it is chosen explicitly with
`--db-engine sqlite` (there is no image to detect it from). Its snapshot is
the database file itself: the migration runs in the disposable app container
as usual, targeting a throwaway file that `{{database}}` points at, and the
result is collected through the app service's bind mount — mounting the
project directory into that service is required. Each worktree then gets the
file copied to `db_path` (default `db.sqlite3`), and only when nothing is
there yet: a database being worked in is never overwritten.

None of this has to be known in advance: `wtm project create <name>` without
`--dir`, and `wtm project edit <name>` without any flag, ask one question at a
time, offer the services found in the project's compose file, and only bring up
the migration command once the backup is enabled. Every question defaults to
what the project already has, so editing is a run of empty answers plus the one
field being changed. Flags and questions mix: what is passed on the command
line becomes the answer offered by default. `--no-input` turns a missing value
into an error rather than a question, for scripts and CI.

`wtm project edit` never touches the port offset nor the recorded worktree
indices, which is what re-registering a project would do: every running stack
would change ports and compose project name. The settings can also be reviewed
directly in `config.json`.

The base branch is the one question whose empty answer records nothing: a
project that names none follows `default_base_branch` from `config.json`, and
`develop` when that is unset too. Answering it pins the project instead
(`--base` does the same), which is what you want for the odd repository that
branches off something else.

`--git-container` is only useful for projects whose compose bind-mounts the
git-dir into a container; left off, it creates nothing.

`backup list` also reports how far each dump has fallen behind, by counting the
commits touching migrations since the revision recorded next to it, and `create`
says so when replaying that delta would cost time. A stale dump still works, the
application simply migrates on top of it, so this is never a blocker. The
migration files are found through `migrations_path` (`--migrations-path`),
whose default matches Django, Prisma and MikroORM alike. A layout it does not
match, `db/migrate/*` for Rails or `src/main/resources/db/migration/*` for
Flyway, has to say so: no commit ever touches the default pathspec there, so
every dump would be reported as up to date forever.

## How ports are isolated

Every published port is rebased so a worktree stack never fights the main one:
`20000 + project offset + default port + worktree index * stride`, with a
fallback above 65535. Ports declared as `${VAR:-default}` are written into the
worktree's `.env`, and ports written as literals into a generated compose file
that replaces the service's `ports` list, since compose appends otherwise.

The index is allocated when the worktree first needs a stack and recorded in
`config.json` (`worktree_indices`), so it never changes afterwards. Deriving
it from `git worktree list` order — what earlier versions did — renumbered
worktrees whenever a new one sorted before them, handing a new stack the very
ports of a running neighbour. A worktree created before indices were recorded
is backfilled from the compose project label its containers and volumes still
carry, or from its listing position when docker holds no trace. A freed index
is only reused once docker — when it can be asked — no longer holds any
container or volume of a previous worktree there; while docker is
unreachable, wtm resolves indices in memory and records nothing.

Three details earned their place the hard way. The reference is the project's
*merged* compose configuration, not just the base file: a project already
remapping a port on purpose keeps that mapping. The project offset exists
because the formula otherwise knows nothing about the project, so two projects
whose database listens on 5432 would land on the same host port; it is assigned
at registration, and the first project keeps 0 so its existing worktrees are
untouched. And a `.env` tracked by git is left alone, the generated compose
file carrying the ports instead, so starting a stack never dirties the
worktree.

## Diagnostics

```sh
wtm doctor
```

Reports, in order: the version running and whether a newer one is published,
the config and backups paths, the memory actually used by the Docker VM, the
size of the build cache, then per project its port stride, offset and database
engine. Below that come the problems nothing else mentions: the ports two
projects would both publish, and what removed worktrees left behind, their
volumes (which squat the indices their ports came from) and the images their
stacks built. Each of those lines carries the command that drops them.

The build cache is only ever reported. Buildkit attributes none of it to a
project, so only you can decide it is expendable.

Before starting a stack, the tool compares the Docker VM's measured usage
against its capacity and warns (without blocking) if one more stack risks
saturating it. The estimate is based on the average observed consumption of
running stacks, never on declared `mem_limit`s: one stack here declares
over 13 GB of cumulative caps while actually running in 2 GB. Ephemeral
`compose run` containers are excluded from that average.

## How the database restore works

A worktree starting from an empty database replays the entire migration
history, which on a large project means tens of minutes and a memory peak
that can get the container OOM-killed. `backup refresh` records that work
once as a dump, and every worktree restores it in seconds.

The dump holds the database exactly as `migrate` left it, data included: not
just the schema, but everything the migrations create, permissions and
reference data among them. It stops there. Seed data is not in it, because
seeds change far more often than migrations and are quick to replay, whereas
the migration history is not. A fresh worktree therefore starts with a
migrated database and still gets seeded, by its `post_create` or by hand
through `wtm exec`. Without a `post_create`, it says so on its own: a stack
whose database is brand new prints a reminder, once, with the command to run.
Measured on a real project, seeding took 33 seconds against the twenty minutes
the migration history costs.

Nothing is asked of the project. wtm links `.db-snapshot` to the central
backup directory, writes a restore script next to the dump, and generates a
`.wtm-snapshot.yaml` in the worktree that mounts both into the database
service. That file is passed to docker as an extra `-f` when the stack starts,
after the project's own compose files.

Creation lays those down and so does every `start`, links, `*.env` copies and
compose overrides alike: they belong to the stack rather than to the checkout,
and a worktree that lost them, cut by an older wtm or cleaned up by hand, would
otherwise fail deep inside docker on a raw mount error. What the worktree
already holds is left as it is, an env file tweaked for the task at hand being
its own state and not a stale copy.

None of it belongs in a commit, and a project's `.gitignore` knows nothing
about names wtm invented, so wtm records them in the repository's
`.git/info/exclude`: not versioned, hence nothing to leak into the project's
history, and read from the common git-dir, so the same block covers every
worktree plus the `.worktrees` directory and the `.git-container` link left in
the main checkout. A repository that refuses that write still gets its stack,
with a warning.

Putting the mount in the project's compose instead would tie the behaviour
to the branch the worktree was cut from, since that file is versioned.

A fresh worktree still needs its seed, which the dump deliberately leaves out.
`post_create` plays it on its own, and `wtm exec` is the way in by hand: either
beats reaching the container yourself, which means knowing the compose project
name derived from the repository, the index and the branch. `wtm create
--no-post-create` leaves it out for one worktree, for a stack wanted quickly
rather than seeded, and prints the `wtm exec` line that plays it later.

Before a stack goes up, wtm measures what the memory it has to fit into already
holds and says when one more would not. On a native Linux docker that pool is
the whole machine, session and browser included; Docker Desktop gives the
containers a budget of their own. When it is tight, a create or a start asks
whether to go ahead, and answering no leaves the worktree without its stack, to
be started later with `wtm start`. The question only comes on a terminal: the
estimate is an average over the running stacks, worth a person's judgement and
never worth failing a script or an agent over. `--ignore-memory` answers it in
advance, for an automation that runs under a terminal with nobody behind it.

`wtm create --run` and `--exec` play one shell line each once the worktree is
ready: `--run` on your machine from the worktree directory, with the compose
environment `wtm run` sets, `--exec` in the application container after the
project's own `post_create`. Both may be given at once, `--exec` first so a
fixture depending on the seed finds it, and `--run` last since it is the one
taking over the terminal. A failure is a warning naming the line that replays
it, never a failed creation. Neither is remembered: they are flags of one
`create` and never reach `config.json`, so nothing in a repository can hand
wtm a command to play on its own. What the command itself does is another
matter, and `--run` runs on the host with your rights, in a checkout of a
branch that may not be yours: creating someone else's branch and playing its
`package.json` scripts in one line is exactly as trusting as it sounds.
`--exec` is bounded by the container.

The official postgres, mysql, mariadb and mongo images all run
`docker-entrypoint-initdb.d` only on an empty data directory, which gives the
right semantics for free: a fresh worktree restores, an existing one is
untouched, and no race with the application's own migrations is possible
since the database reports healthy only once the restore is done.

## Storage

| What | Where |
|---|---|
| Project registry | `~/.config/wtm/config.json` |
| Dumps + metadata | `~/.config/wtm/backups/<project>/<project>.dump` |
| Worktree indices | `~/.config/wtm/config.json`, `worktree_indices` per project |
| Registry write lock | `~/.config/wtm/config.lock`, an OS file lock; the file staying around is normal |

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

1. `wtm create <project> <branch> --no-start` -> the worktree contains the
   `*.env` files, the `.git-container` and `.db-snapshot` symlinks.
2. Without `--no-start` -> the stack starts and the allocated ports are
   displayed.
3. `wtm backup refresh <project>` with the stack stopped -> it starts on
   its own, the dump shows up in `backup list`.
4. `wtm stop` then `remove` -> stack stopped, worktree removed, local
   branch still present.

## Contributing

Pull requests are welcome. `main` is the only long-lived branch: branch off it,
keep the branch short, open a PR. CI runs `go test -race`, `go vet`, `gofmt`,
staticcheck and a build, and has to be green to merge. What changed between
published versions is in [CHANGELOG.md](CHANGELOG.md).

The test suite needs neither git, docker nor node: every external command goes
through an injectable runner, which keeps it fast and hermetic. Please keep it
that way.

Publishing a version is a changelog entry and a tag:

```sh
# CHANGELOG.md: turn Unreleased into the version being cut, then commit it
git tag -a v0.4.0 -m "One line on what this version brings"
git push origin v0.4.0
```

The tag is what publishes: the module proxy resolves it, so `go install
...@v0.4.0` works straight away. Pushing it also runs the release workflow,
which creates the GitHub release with that version's changelog section as its
notes and the tag annotation as its title. A tag with no section in the
changelog fails the workflow rather than getting notes invented from the commit
log; document it, then replay the run from the Actions tab.

## License

MIT, see [LICENSE](LICENSE).
