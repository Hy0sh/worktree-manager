# Changelog

What changed between published versions, and why. Versions follow
[semantic versioning](https://semver.org): while the major stays at 0, a minor
bump carries new commands or new behaviour, a patch bump carries fixes.

## [Unreleased]

## [0.4.2] - 2026-08-23

### Fixed

- Every checkout-controlled write — worktree provisioning, `.env`
  generation, port rewrites, snapshot restores — now goes through the new
  `internal/safefile` package, which refuses to write through a symlink
  standing at the destination.
- An `.env` symlink is only followed, and recreated as a symlink, when its
  target resolves inside the project; one pointing outside is now skipped
  with a warning instead of being copied through.
- Rebasing a port for a worktree keeps the original host interface prefix
  (e.g. `127.0.0.1:`) instead of dropping it and binding the service to
  every interface. IPv6 `host_ip` bindings are still not rebased, unchanged
  from 0.4.x.
- Engine detection recognises more known server image variants and now
  warns and asks on an image it cannot place, instead of silently
  defaulting to postgres.
- `forceSymlink` no longer refuses to replace a directory that holds only
  Finder's `.DS_Store` noise.
- `app_service` is validated on every path that can set it — creation,
  edit and the stepper alike — and re-validated at generation time; its
  temporary override file now lives under wtm's own directory instead of
  the shared system temp directory.
- `backup rm` deletes the backup directory again: the refresh lock file
  staying on disk no longer keeps an otherwise-empty directory behind.

## [0.4.1] - 2026-08-22

### Fixed

- Copies into a worktree no longer follow symlinks, in either direction. A
  branch tracking `.env` as a symlink, even one escaping the worktree, had
  the developer's env values written through the link at a path the branch
  chose, and the worktree never got its own file; the copy now replaces the
  link with a regular file. And a dangling source symlink, typically `.env`
  pointing at an `.env.local` not created yet, failed the whole create on a
  stat error instead of being skipped with a warning.
- The `.db-snapshot` and `.git-container` links no longer destroy real
  content standing at their path. Replacing the empty directory trees Docker
  materialises at missing bind-mount sources still works; a tracked file or
  a directory with files is now refused with a conflict message instead of
  being deleted.
- Refreshes of one project are serialised through a per-project lock: two at
  once fought over the same throwaway database and, for sqlite, the same
  temporary file. The second one fails fast rather than queue up to redo the
  first one's work.
- Engine detection matches the image's exact basename against an explicit
  list per engine, instead of a substring: `mysql-proxy` was taken for mysql
  and `mongo-backup-sidecar` for mongodb, and a `--no-input` registration
  recorded that wrong engine without any question asked.

## [0.4.0] - 2026-08-22

### Added

- MySQL, MariaDB and MongoDB join Postgres for the pre-migrated backup. Each
  engine lives behind one interface (readiness probe, throwaway database,
  dump command, generated restore script), and the central mechanism is
  untouched: the official images share Postgres' run-`initdb.d`-only-on-empty
  semantics. The engine is recorded as `db_engine` in the project's backup
  settings, empty meaning postgres so nothing changes for existing
  registrations; at registration it is detected from the database service's
  compose image and offered as the default, `--db-engine` overrides it, and
  `wtm doctor` shows it per project. Credentials are never stored nor passed
  in wtm's arguments: every engine command reads them from the container's
  own environment (`MYSQL_ROOT_PASSWORD`, `MONGO_INITDB_*`), expanded inside
  the container.
- SQLite, as the first file-based engine: no service, no probe, the snapshot
  is the database file itself. The migration still runs in the disposable app
  container, targeting a throwaway file collected through the app service's
  bind mount, and each worktree gets it copied to `db_path` (default
  `db.sqlite3`, `--db-path` to change it) only when nothing is there yet: a
  database being worked in is never overwritten. Chosen explicitly with
  `--db-engine sqlite`, there being no image to detect it from.
- This changelog.

### Fixed

- Every compose override name the detection knows is now copied into the
  worktree. Provisioning kept its own list of two names out of four, so a
  project using `compose.override.yml` or `docker-compose.override.yaml` had
  its override referenced by `-f` against the worktree without existing
  there, and `docker compose up` failed on a missing file.
- The registry lock is held through the kernel (flock, LockFileEx on Windows)
  instead of a lock file with a staleness heuristic. A killed wtm can no
  longer leave the registry locked, and the stat-then-remove takeover, which
  could delete the fresh lock a concurrent wtm had just taken, is gone with
  the whole stale concept.
- The migration environment values, typically a `DATABASE_URL` carrying a
  password, no longer appear in the docker arguments that error messages
  quote: docker reads a bare `-e KEY` from its own environment, so the values
  travel there instead.
- A `.env` created from scratch in a worktree is written `0600`. An existing
  file keeps its own permissions.
- The mysql readiness probe is an authenticated query, not `mysqladmin ping`:
  the image's init phase answers pings on the socket while root is not usable
  yet, and commands sent then were refused.

### Changed

- Compose files are parsed as YAML instead of line regexes. The old parser
  assumed two-space indentation and the short `ports:` syntax only, so a
  service using `target`/`published` or another indentation silently lost its
  port isolation; anchors and compose's `!override` tags now pass through
  too.
- `SECURITY.md` spells out the trust model: compose files run with Docker's
  power, so registering a repository means trusting it, and dumps carry the
  development database as it is, with no anonymization.
- Two dependencies join cobra: `gofrs/flock` and `gopkg.in/yaml.v3`. Nothing
  changes at runtime, `git` and `docker` remain the only tools required.

## [0.3.0] - 2026-08-17

### Added

- `wtm project edit <name>` changes a registered project, its backup settings
  included, touching only the flags it is given and printing what moved. Until
  now the only ways were to hand-edit `config.json` or to remove and re-register
  the project, which hands out a fresh port offset and forgets the recorded
  worktree indices: every stack already running would change ports and compose
  project name. `edit` never touches those two.
- `project create` without `--dir`, and `project edit` without any flag, ask for
  the settings one question at a time instead of expecting ten flags to be known
  in advance. The services declared in the project's compose file are offered
  when naming the database and application services, the migration questions
  only come up once the backup is enabled, and every question defaults to the
  current value. Flags and questions mix: what is passed on the command line
  becomes the answer offered by default. `--no-input` turns a missing value into
  an error, for scripts and CI.

### Fixed

- The files wtm drops beside a checkout (`.git-container`, `.db-snapshot`,
  `.wtm-snapshot.yaml`, `.wtm-ports.yaml`, and `.worktrees` in the main
  checkout) are recorded in the repository's `info/exclude`, so `git add -A` no
  longer stages them, `.worktrees` as an embedded git repository at that. That
  file is not versioned and is read from the common git-dir, so nothing reaches
  the project's history and one write covers every worktree.

### Changed

- Every source file now carries a single notion and has its `_test.go`
  counterpart: `worktree.go` had grown to 825 lines holding the lifecycle, the
  git plumbing, the provisioning, the ports and the docker stack side by side.
- Dead code removed: `composeFileEnv` and `joinComposeFiles` had lost their
  callers when the generated compose file started being passed as an explicit
  `-f`, and `stack.ReadPorts`/`ReadPortValues` when the endpoints started being
  recomputed from the allocations.
- CI fails on unused code (staticcheck), which `go vet` does not report.

## [0.2.1] - 2026-08-17

### Fixed

- The stack artifacts are laid down again on every `start`, not only at
  creation: the `*.env` copies, the compose overrides, the `.git-container` and
  `.db-snapshot` links. A worktree that had lost them, cut by an older wtm or
  cleaned up by hand, failed deep inside docker on a raw mount error, and docker
  materialised an empty directory at every missing bind-mount source. What the
  worktree already holds is left alone, an env file tweaked for the task at hand
  being its own state rather than a stale copy.

## [0.2.0] - 2026-08-17

### Added

- Each branch's worktree index is recorded in the registry (`worktree_indices`)
  and reused from there. A worktree created before this gets its index
  backfilled from the compose project label docker still carries, so existing
  stacks keep the ports their `.env` already has.

### Changed

- The index no longer derives from git's listing position. That order resorts
  alphabetically as worktrees come and go, which renumbered running stacks: the
  ports moved and the compose project name became unreachable.
- Registry writes go through a lock, so two concurrent registrations cannot pick
  the same port offset.

### Fixed

- The index resolver degrades instead of guessing when docker cannot be reached:
  a blind allocation is used for the current call only, never recorded, so a
  later resolution can still backfill for real. A partial docker survey (the
  container probe answers, the volume one does not) keeps what it found rather
  than discarding it, since missing volume labels can only understate leftovers.

## [0.1.1] - 2026-08-17

### Fixed

- `wtm create` checks out a branch that only exists on a remote, tracking it and
  fetching first in case it was pushed since the last fetch. It used to cut a
  new branch from base instead: same name, none of its commits, no upstream, and
  a divergence to sort out at the first push. Two remotes carrying the same
  branch name is an error rather than a guess, as git does.
- What reaches a generated script, a generated compose file or a path is
  validated, project name and service names included, since `config.json` can be
  hand-edited.

### Changed

- The go directive was lowered to the version actually needed, so the binary
  installs on older toolchains.

## [0.1.0] - 2026-08-14

First tagged release. The whole worktree lifecycle behind one binary:

- `create`, `start`, `stop`, `remove` and `list`, plus `exec` to run a command
  in the worktree's container, `run` to run one on the host from the worktree
  directory, and `path` for the shell to compose with. Creation sits behind an
  explicit `create` verb: the bare form turned any typo into a branch, a
  mistyped `wtm list` having created a branch named `list`.
- Ports are rebased for every worktree, including those a compose file writes as
  literals, through a generated override. The reference is the merged compose
  configuration, so a project that already remaps a port on purpose keeps it.
- wtm owns the database restore: it links the central dump, writes the restore
  script and generates the compose file mounting both, so nothing is asked of
  the project. `backup list` reports how far a dump has fallen behind by
  counting the commits touching migrations since the revision recorded next to
  it.
- No runtime dependency beyond `git` and `docker`. The dependency on
  worktree-compose was dropped, its port formula and `.env` markers kept
  identical so worktrees created with it keep working.
- A project without a compose file is not an error, there is simply no stack.

[Unreleased]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Hy0sh/worktree-manager/releases/tag/v0.1.0
