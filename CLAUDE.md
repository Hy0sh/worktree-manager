# worktree-manager

## Comments

A comment earns its place only by carrying something the code cannot show:

- an **external constraint** (`docker-entrypoint-initdb.d` only runs on an empty
  data directory, VirtioFS cannot bind-mount a worktree's `.git` pointer file,
  compose appends `ports` when merging);
- a **decision and its why** (the offset exists so two projects' databases do
  not fight over 5432; the lock file staying on disk carries no state);
- a **contract the signature hides** (`emptyTree` returns a path despite its
  name, `removeDBFiles` also drops the `-wal` and `-shm` siblings);
- a **specificity** worth naming: an example, a framework convention, the shape
  of an output being parsed.

Never restate the code. `// Dir is where config.json lives` above `func Dir()`
tells a reader of Go nothing. Neither does `// Load reads the registry`.

Three lines is the ceiling for one block. Past that, the comment is arguing
with itself: keep the constraint, drop the justification of the justification.
The package doc goes in one file, not in every file of the package.

## CI, reproduced locally

Run these before saying anything is done. They are exactly what
`.github/workflows/ci.yml` runs, in the same order:

```sh
go test -race ./...   # the suite never needs git, docker or node: keep it that way
go vet ./...
gofmt -l .            # must print nothing
go build ./...
```

The workflow adds `govulncheck` and `staticcheck`, both fetched on the fly; run
them too when the change touches dependencies or adds a package. A fifth gate,
`testdata/integration/run.sh`, drives the real binary against docker; it runs on
CI only, since it creates a git repository in the fixture and docker state.

## Runtime proof

Unit tests never prove that a generated restore script runs inside a real
engine, that a port lands where wtm says, or that a dump restores. The two
fixtures in `~/dev/projects/wtm-fixtures/{mysql,mongo}` exist for that, are
registered as `fx-mysql` and `fx-mongo`, and each serves `GET /widgets`, which
returns a row only if the worktree came up on the restored snapshot.
