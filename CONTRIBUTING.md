# Contributing

Pawprints is a small Go project plus a thin editor extension. Keep changes
tight and focused.

## Setup

```sh
git clone https://github.com/elythi0n/pawprints.git
cd pawprints
make build          # bin/pawprints
make test
cd extension && npm install && cd ..
```

Requirements: Go (see `go.mod`), git, Node for the extension.

## Before pushing

```sh
make check
```

That runs `go vet`, `staticcheck`, `golangci-lint`, the Go tests with the
race detector, and the extension's lint, tests, and build. CI runs the same
on every push and pull request.

## Code style

- `gofmt` and `goimports` with local imports grouped under `github.com/elythi0n/pawprints`.
- Comments explain why, not what. No journaling comments.
- Errors are lowercase, no trailing period, wrapped with `%w`.
- CLI handlers stay thin: parse flags, call `internal/<feature>`, render.
- TUI panes stay thin: keys and rendering. Logic belongs in the feature packages.
- Every write to the data directory goes through `internal/store`.
- Hook packages never import Bubble Tea or Lipgloss, never panic out, never exit 2.
- `--json` is part of the contract: a new command supports it, and
  `internal/cli/AGENTS.md` documents the shape.

## Tests

Table-driven and hermetic. Set `PAWPRINTS_HOME` and `CLAUDE_CONFIG_DIR` to
temp directories; the store tests show how. TUI tests drive the model with
messages and check every frame fits the terminal.

## Releasing

1. Update `CHANGELOG.md`.
2. `scripts/stamp-version.sh vX.Y.Z`, commit, `git tag vX.Y.Z`.
3. Push the commit and the tag. The Release workflow checks the tag matches
   the stamped versions, runs `make check`, builds, and publishes the GitHub
   release with the binary, the vsix, checksums, `install.sh`, and `VERSION`.
   A `## X.Y.Z` section in `CHANGELOG.md` becomes the release notes.
4. On another machine: run `install.sh`, then `pawprints doctor`.

`make publish` does the same from your machine, with the GitHub CLI (`gh`)
signed in.

Tags are always pushed by a person.
