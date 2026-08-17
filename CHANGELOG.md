# Changelog

What changed between published versions, and why. Versions follow
[semantic versioning](https://semver.org): while the major stays at 0, a minor
bump carries new commands or new behaviour, a patch bump carries fixes.

## [Unreleased]

### Added

- This changelog.

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

[Unreleased]: https://github.com/Hy0sh/worktree-manager/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/Hy0sh/worktree-manager/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/Hy0sh/worktree-manager/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Hy0sh/worktree-manager/releases/tag/v0.1.0
