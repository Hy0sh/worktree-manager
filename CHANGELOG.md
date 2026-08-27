# Changelog

What changed between published versions, and why. Versions follow
[semantic versioning](https://semver.org): while the major stays at 0, a minor
bump carries new commands or new behaviour, a patch bump carries fixes.

## [Unreleased]

### Added

- `ready_timeout` and `ready_interval` (`--ready-timeout`, `--ready-interval`)
  bound the wait a new worktree grants each service before `post_create` runs.
  Durations, as in `--ready-timeout 2m --ready-interval 10s`. The built-in
  bounds are unchanged when they are unset: a minute for the database, ten for
  the application, asked every second. A malformed duration is refused by the
  flag rather than discovered halfway through a `create`, and the stepper does
  not ask, since registering a project should not have to answer for a setting
  this rare.

### Fixed

- A command called without its arguments says what is missing and shows a call
  that works, instead of counting what it received: `wtm project create` answered
  `accepts 1 arg(s), received 0`, which names neither the project nor the form of
  the command. Ten commands were doing it. A test walks the command tree, so one
  added later cannot go back to counting.
- `post_create` waits for the application service to report itself healthy, and
  no longer for the database alone. A stack that installs its dependencies from
  its `command:` (`poetry install`, `npm ci`) has a container docker calls
  started well before it can run a management command, so the seed died on
  `ModuleNotFoundError: No module named 'django'` while the install was still
  running. A service that declares no `healthcheck:` is waited on by its first
  published port instead, read through `/proc/net/tcp` in the container's own
  network namespace: the host side of a published port accepts a connection long
  before anything listens behind it, so it proves nothing. Only a service with
  neither, a queue worker for instance, leaves nothing to wait for: there the
  command runs as it did before and a warning names what is missing.
- A `create` that seeds no longer buries the addresses of the stack: the seed's
  output can be hundreds of lines, so the list is printed again once the command
  is done. And a wait that holds says so every thirty seconds, with the time
  elapsed, instead of leaving a single line at the top of several minutes of
  silence.
- Commands printed in a failure message are quoted the way a shell takes them
  back. A `post_create` chaining two commands with `&&` was reported as
  `sh -c python manage.py seed && python manage.py users`, which reads as a
  quoting bug that is not there, and its `wtm exec` replay line pasted bare left
  the tail of the chain to the user's own shell instead of the container. The
  replay now names `sh -c` explicitly, since that is how the command is played.

## [0.6.0] - 2026-08-27

### Added

- `migrations_path` (`--migrations-path`) is now asked for by the stepper and
  settable by flag. It decides whether `backup list` and `create` report a dump
  as stale, and its default pathspec matches Django, Prisma and MikroORM alone:
  a Rails project (`db/migrate/*`) or a Flyway one had its dump called up to
  date forever, since no commit ever touches `*migrations/*` there. Editing
  `config.json` by hand was the only way in. The answer is recorded only when it
  differs from the default, so an edit of a project registered earlier does not
  report a change it did not make.

### Fixed

- The stepper no longer pins `base_branch` on a project that inherits it. It
  offered the inherited branch as the default answer and wrote it straight back,
  so every project registered through the questions ended up with its own
  `base_branch` and `default_base_branch` applied to nobody. An empty answer now
  records nothing, and the prompt says what is being inherited
  (`base branch [inherited: main]`).

## [0.5.0] - 2026-08-25

### Added

- `post_create` (`--post-create`) is the command a project plays in its
  application service once a new worktree's stack answers. The dump carries what
  the migrations create and never seed data, so a fresh worktree came up migrated
  and empty and every developer seeded it by hand, usually by reaching for the
  project's own reset script: on gallia that script drops the schema and migrates
  again, which throws away the restored dump and pays for the migrations wtm
  exists to skip. The database is waited for with the probe `backup refresh`
  already used, extracted as `execx.WaitFor`. A failure is a warning naming the
  `wtm exec` line that replays it: the worktree exists and works, and losing it
  over a seed would be the worse outcome. The note telling you to seed by hand no
  longer prints when a `post_create` is about to do it.

- `wtm doctor` opens on the version of the running binary, and says when a newer
  one is published. The number comes from the Go module proxy, which answers
  what `go install ...@latest` would resolve and needs no token, where the
  GitHub API rate-limits anonymous callers. A `wtm upgrade` command was the
  other option and was dropped: it would have been a wrapper around one
  `go install` line, and knowing one is behind is the part nothing provided.
  The check follows the rule that made this idea unacceptable when it was first
  raised: a read command never blocks on the network, so it has a two second
  budget, degrades to silence on a timeout, an unreachable proxy or an answer it
  cannot read, and says nothing about a build made from a working copy.

### Fixed

- `wtm run` sets `COMPOSE_PROJECT_NAME` and `COMPOSE_FILE`, so a script of the
  project calling `docker compose` reaches the worktree's stack. It used to
  reach a project named after the directory it ran from, which exists nowhere:
  `docker compose exec` found no container, and `docker compose up` created a
  third stack on the project's default ports, colliding with the main one. The
  file list matters as much as the name, since `.wtm-ports.yaml` and
  `.wtm-snapshot.yaml` are not named `override` and compose loads neither on its
  own. The index is read from the registry, not resolved: `wtm run` stays on the
  host and keeps working with docker stopped.
- The README called the same placeholder `my-project` under `project create` and
  `my-app` everywhere else, which read as two different things.

## [0.4.8] - 2026-08-25

### Fixed

- `wtm remove` drops the images compose built for the stack, which nothing ever
  did: `docker compose down` keeps them exactly as it keeps volumes, and every
  worktree builds its own copy of every service image under its own project
  name. One machine had 138 of them for 35 worktrees long removed, and dropping
  them freed 12 GB, the rest of their weight being layers the live images share.
  Only what compose labelled with the stack's project goes: a pulled image
  carries no such label, so the `postgres` the main stack also runs cannot be
  caught.
- `wtm doctor` reports those images for the worktrees that no longer exist, next
  to the volumes it already reported, and the size of the build cache. The cache
  is only ever reported: buildkit attributes none of it to a project, so nothing
  but the developer can decide it is expendable.

## [0.4.7] - 2026-08-24

### Fixed

- A worktree whose HEAD was detached, by a `git checkout <rev>` made inside it,
  is no longer a listing with an empty `BRANCH`, no index and no way back: git
  names no branch for it, and every command looks a worktree up by branch. The
  branch is now read off the path, which wtm alone chose as
  `<repo>/.worktrees/<branch>`, so the index, the status and `wtm up|stop|remove
  <branch>` work again, `wtm list` shows `<branch> (detached <rev>)`, and the
  commands that act on the worktree say what runs there before doing so.

## [0.4.6] - 2026-08-24

### Fixed

- `wtm list` and the completion no longer show worktrees wtm did not create, and
  `wtm project remove` is no longer trapped by one. Only `<repo>/.worktrees/`
  holds what every command needs (a stable index, a provisioned `.env`, a
  stack), so a `claude -w` worktree under `.claude/worktrees` was listed with a
  `down` status for a stack that never existed, and since 0.4.5 it also blocked
  the removal of the registry entry while demanding a `wtm remove` that could do
  nothing for it. Those worktrees now belong to whatever created them.
- `wtm remove` can remove a locked worktree, which it never could: git refuses
  one even with a single `--force`, and overriding a lock takes `remove -f -f`.
  The lock is read from the listing and checked before anything is taken down,
  alongside the tracked changes: without `--force` the removal is refused,
  naming the lock holder and leaving the stack up, and with it the second
  `--force` goes through.

## [0.4.5] - 2026-08-23

### Changed

- `wtm project remove` refuses while the project still has worktrees, listing
  them. The order matters and used to be silent: every worktree command needs
  the registry entry, so once it is gone those worktrees can only be removed by
  editing `config.json` by hand, and the port offset the removal frees is handed
  to the next registered project, whose stacks would then fight their ports. A
  directory git cannot answer for does not block, since nothing could be
  verified and the entry would be trapped.

## [0.4.4] - 2026-08-23

### Fixed

- `wtm doctor` no longer reports a database engine for a project that has no
  database: the backup settings default to postgres whether or not a dump is
  enabled, so every project without one was listed as running postgres. Those
  now read `-`, like the other columns with nothing to show.
- `wtm doctor`'s own description no longer mentions resolving `wtc`, a
  dependency dropped several versions ago. It says what the command actually
  reports: the configuration, the Docker VM, and what removed worktrees left
  behind.

### Changed

- Comments that restated the code are gone, and the convention that keeps them
  out is written down in `CLAUDE.md`: a comment carries an external
  constraint, a decision, a contract the signature hides or a specificity, in
  three lines at most. No behaviour changed.

## [0.4.3] - 2026-08-23

Everything here came out of running the whole lifecycle against real projects
rather than the test suite: seven behaviours that only show once a migration,
a dev server or a second project is involved.

### Fixed

- A refresh no longer publishes a dump of a throwaway database the migrations
  never reached. The `{{database}}` mechanism only works if the app honours the
  variable it is mapped to; when it does not, the migrations run against the
  project's own database and the throwaway one stays empty. That empty dump
  used to be announced as a success and brought every worktree up on an empty
  database, discovered at the first start. The count is a safety net, not a
  gate: only a database that reads as empty fails, a probe wtm cannot run or
  cannot parse is a warning.
- Removing a backup drops its directory again. Beside the dump, wtm keeps the
  refresh lock and, once a worktree has started, the restore script; the
  0.4.2 fix only accounted for the lock, so the directory survived holding the
  script. Anything in there that is not wtm's own still keeps it, as before.
- Registering a directory that is not a git repository is refused on the spot.
  It used to be accepted, then failed a refresh at its very last step, after
  the image was built and the database dumped.
- A refresh whose metadata cannot be written is no longer a failed refresh.
  The dump is already published and usable; the metadata only feeds the
  "how far behind" heuristic of `backup list`, so it degrades to a warning.
- `wtm remove` checks for uncommitted changes before taking the stack down.
  Refusing after the stack was already stopped left the worktree in place and
  the developer's dev server gone, for a removal that never happened. The
  message now says the stack is still running.
- `project create` and `project edit` warn when the compose file pins a
  `container_name`. Ports, volumes and the compose project name are rebased;
  a fixed container name is not, so the main stack and a worktree stack cannot
  both run, and docker only said so at the first start.
- `wtm doctor` reports what nothing reported before: the ports two projects
  would both publish (the per-project offset step is smaller than the spread
  of the default ports it shifts, so stacks of different projects can collide,
  with a docker bind error naming neither), and the volumes of removed
  worktrees, which squat the indices their ports came from and push every new
  worktree further out.
- Cancelling `project remove` says how to answer for a script, since a closed
  input reads as a no.

## [0.4.2] - 2026-08-23

### Fixed

- Every checkout-controlled write — worktree provisioning, `.env`
  generation, port rewrites, snapshot restores — now goes through the new
  `internal/safefile` package, which refuses to write through a symlink
  standing at the destination.
- An `.env` symlink is only followed when its target resolves inside the
  project, copying the values behind it into a regular file as before; one
  pointing outside is now skipped with a warning instead of being copied
  through.
- Rebasing a port for a worktree keeps the original host interface prefix
  (e.g. `127.0.0.1:`) instead of dropping it and binding the service to
  every interface. IPv6 `host_ip` bindings are still not rebased, unchanged
  from 0.4.x.
- Engine detection recognises more known server image variants and now
  warns on an image it cannot place, instead of silently defaulting to
  postgres without saying so.
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

[Unreleased]: https://github.com/Hy0sh/worktree-manager/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.8...v0.5.0
[0.4.8]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.7...v0.4.8
[0.4.7]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.6...v0.4.7
[0.4.6]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.5...v0.4.6
[0.4.5]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.4...v0.4.5
[0.4.4]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Hy0sh/worktree-manager/releases/tag/v0.1.0
