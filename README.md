# worktree-manager

Go CLI that creates a ready-to-use git worktree and manages its full
lifecycle: creation, stack startup, stop, removal, plus a pre-migrated
Postgres backup that makes bootstrapping a database fast.

It has no runtime dependency beyond `git` and `docker`: port allocation,
compose project naming and the stack lifecycle are all handled directly.
Port assignments follow the same formula
[worktree-compose](https://github.com/mostafasudo/worktree-compose) used, and
the same `.env` markers, so a worktree created with that tool keeps the ports
it already had.

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

## Prerequisites

- `git`, `docker` / `docker compose` on PATH.
- Nothing to install on the target project's side.
- Nothing about the project's ports either: those written as `${VAR:-default}`
  are rebased through the worktree's `.env`, those written as literals through
  a generated compose file. A port stride can be set in `.wtcrc.json` or under
  `wtc` in `package.json`.
- For `dump: true`: nothing on the project's side. wtm generates the compose
  file that mounts the dump and ships the restore script itself, so the
  behaviour never depends on the branch a worktree was cut from.

## Usage

```sh
# register a project (once)
wtm project create my-project --dir ~/dev/projects/my-project --base develop
wtm project list

# create a worktree + start its stack
wtm create my-app feat/my-branch            # base = the project's own
wtm create feat/my-branch                   # project = current repo
wtm create feat/my-branch main              # explicit base
wtm create feat/my-branch --no-start        # without starting the stack

# lifecycle
wtm list                                    # worktrees of the current project
wtm start feat/my-branch                    # bring a stopped stack back up
wtm stop feat/my-branch
wtm remove feat/my-branch                   # local branch kept
wtm remove feat/my-branch --force           # despite modified tracked files

# inside the worktree's containers
wtm exec feat/my-branch -- python manage.py seed_data
wtm exec feat/my-branch -- bash
wtm exec feat/my-branch --service db -- psql -U postgres

# on your machine, from the worktree directory
wtm run feat/my-branch -- claude
wtm run feat/my-branch -- git status
cd $(wtm path feat/my-branch)

# Postgres backup
wtm backup refresh my-app            # starts db+backend if needed
wtm backup list
wtm backup remove my-app
```

If the first argument is a registered project, it's treated as such;
otherwise it's a branch of the current directory's project.

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
defaults to `db` and `--db-user` defaults to `postgres`. These settings can
be reviewed and tweaked directly in `config.json`.

`--git-container` is only useful for projects whose compose bind-mounts the
git-dir into a container; left off, it creates nothing.

## How ports are isolated

Every published port is rebased so a worktree stack never fights the main one:
`20000 + project offset + default port + worktree index * stride`, with a
fallback above 65535. Ports declared as `${VAR:-default}` are written into the
worktree's `.env`, and ports written as literals into a generated compose file
that replaces the service's `ports` list, since compose appends otherwise.

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

Shows the config and backups paths, the memory actually used by the Docker
VM, the port stride of each registered project, and any compose file whose
ports are hardcoded and therefore cannot be isolated.

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
once, as a schema-only dump; every worktree then restores it in seconds.

Nothing is asked of the project. On creation, wtm links `.db-snapshot` to
the central backup directory, writes a restore script next to the dump, and
generates a `.wtm-snapshot.yaml` in the worktree that mounts both into the
database service. That file is handed to docker through `COMPOSE_FILE` when
the stack starts, on top of the project's own compose files.

Putting the mount in the project's compose instead would tie the behaviour
to the branch the worktree was cut from, since that file is versioned.

The dump carries the schema and the migration table, never data, so a fresh
worktree still needs its own seed. `wtm exec` is the way in: reaching the
container by hand means knowing the compose project name derived from the
repository, the index and the branch, which is internal knowledge.

Postgres only runs `docker-entrypoint-initdb.d` on an empty data directory,
which gives the right semantics for free: a fresh worktree restores, an
existing one is untouched, and no race with the application's own migrations
is possible since the database reports healthy only once the restore is done.

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

1. `wtm create <project> <branch> --no-start` -> the worktree contains the
   `*.env` files, the `.git-container` and `.db-snapshot` symlinks.
2. Without `--no-start` -> the stack starts and the allocated ports are
   displayed.
3. `wtm backup refresh <project>` with the stack stopped -> it starts on
   its own, the dump shows up in `backup list`.
4. `wtm stop` then `remove` -> stack stopped, worktree removed, local
   branch still present.
