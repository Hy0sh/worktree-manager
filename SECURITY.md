# Security policy

## Reporting a vulnerability

Please report security issues privately through
[GitHub's advisory form](https://github.com/Hy0sh/worktree-manager/security/advisories/new)
rather than a public issue, and allow some time for a fix before disclosing.

## What this tool does on your machine

`wtm` is a local developer tool. It runs `git` and `docker` on your behalf. Its
only network request of its own is `wtm doctor` asking the Go module proxy
(`proxy.golang.org`) for the latest published version.

Two files are treated as trusted input, because whoever can write them already
controls the machine:

- `~/.config/wtm/config.json`, whose `migrate_command` and `deps_command` are
  executed inside a container by design. It may also hold a connection string
  with a password, so it is created with `0600` permissions, in a `0700`
  directory.
- The target project's compose files, which are read to know which ports to
  rebase, and then handed to `docker compose`. A compose file is code running
  with Docker's power over the machine (bind mounts, privileged containers,
  the Docker socket), so registering a repository with `wtm` means trusting
  it; do not point it at a repository you would not `docker compose up`
  yourself. A worktree's stack runs the compose files of the branch checked
  out in it, so `wtm create <branch>` trusts that branch's compose files too.

Project and service names are validated against `[a-z0-9_-]` when a project is
registered, and again when they are interpolated into the generated restore
script and compose files. Branch names are refused when they start with `-`,
since they reach `git` as arguments.

Database dumps under `~/.config/wtm/backups` carry everything the migrations
create, reference data included. The dump and its project directory are
readable by all (`0644` and `0755`), because the database container reads them
as its own user; what keeps them from other accounts is the backups directory
itself, which `wtm` resets to `0700` whenever it writes into it. They are taken from
your development database as it is, with no anonymization: if that database
holds personal or client data, so does the dump, and it stays on disk until
the backup is refreshed or removed.
