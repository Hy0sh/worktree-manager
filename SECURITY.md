# Security policy

## Reporting a vulnerability

Please report security issues privately through
[GitHub's advisory form](https://github.com/Hy0sh/worktree-manager/security/advisories/new)
rather than a public issue, and allow some time for a fix before disclosing.

## What this tool does on your machine

`wtm` is a local developer tool. It runs `git` and `docker` on your behalf, and
it makes no network request of its own.

Two files are treated as trusted input, because whoever can write them already
controls the machine:

- `~/.config/wtm/config.json`, whose `migrate_command` and `deps_command` are
  executed inside a container by design. It may also hold a connection string
  with a password, so it is created with `0600` permissions, in a `0700`
  directory.
- The target project's compose files, which are read to know which ports to
  rebase.

Project and service names are validated against `[a-z0-9_-]` when a project is
registered, and again when they are interpolated into the generated restore
script and compose files.

Database dumps under `~/.config/wtm/backups` carry everything the migrations
create, reference data included, and are written `0600`.
