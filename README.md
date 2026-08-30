# Hermes

A **native, open source desktop database manager** for PostgreSQL, written in Go.

Fast, lightweight clients (TablePlus, Postico, Beekeeper) can't compare structure, compare
data, transfer between databases, or run backups. The tools that can (Navicat, DBeaver PRO,
dbForge) are heavy, expensive, or both. Hermes exists to fill that gap: the four operations
people buy a license for — **structure sync, data sync, data transfer, and integrated
backup/restore** — in a native app that is fast and free.

The bet is focus: do for **one** engine what the competition tries to do for twenty.

## Project status

**Pre-alpha — under active development, no usable release yet.** This README grows with every
feature shipped: anything documented below with an example works. Anything in the *Roadmap*
section does not.

## Principles

- **Nothing destructive without a preview.** Every operation that changes structure or data
  produces SQL you can read, edit, and save before it runs.
- **Whatever the wizard does, the CLI does too.** Every operation is a serializable profile
  that runs headless — the same file works on your machine and in CI.
- **Nothing blocks the UI.** Every long operation is a cancelable job with progress.
- **Fast is a requirement, not a goal.** Startup under 1.5s; first page of a 50M-row table in
  under 500ms; diff of 1,000 tables in under 20s.
- **A false positive in the diff is a bug.** Comparing a schema against itself must report
  zero differences.

## Requirements

- **PostgreSQL 12 or newer** on the server you want to manage.
- **PostgreSQL client tools** (`pg_dump`, `pg_restore`, `psql`) for the backup and restore
  features. Hermes does **not** bundle these binaries: it detects your existing installation
  and warns you when the client version is incompatible with the server. Install them with
  your platform's package manager:

  ```sh
  # Debian / Ubuntu
  sudo apt install postgresql-client

  # macOS (Homebrew)
  brew install libpq

  # Windows (winget)
  winget install PostgreSQL.PostgreSQL
  ```

## Installation

### Binaries

Signed installers for Linux, macOS, and Windows will be published on the Releases page
starting with version 1.0. Until then, build from source.

### From source

Requires [Go 1.22+](https://go.dev/dl/), [Node.js 20+](https://nodejs.org/), and the
[Wails v3](https://wails.io/) CLI.

```sh
git clone https://github.com/gsoares85/hermes.git
cd hermes

# dependencies and core build
go mod download
go build ./...

# desktop app in development mode
wails3 dev

# package for the current platform
wails3 package
```

The packaged binary lands in `bin/`.

## Features

No features have shipped yet. This section is filled in as each one lands, always with a real
usage example.

## Roadmap

What is being built, in order:

**Foundation** — connect to PostgreSQL 12+ with actionable failure diagnostics, passwords
stored in the OS keychain, lazy object-tree navigation, and a cancelable job engine with
progress reporting.

**Query and data** — SQL editor with cancelable execution, a virtualized grid using keyset
pagination (no `OFFSET` on large tables), and inline editing that shows the `UPDATE` before it
runs.

**Backup and restore** — wizards over `pg_dump` and `pg_restore`, with selective object
restore and a local catalog of the backups you generate.

**Structure sync** — native schema diff over `pg_catalog`, producing a reviewable migration
script, a difference report, and a CI mode that turns the diff into a pipeline gate.

**Data transfer and sync** — move and reconcile bulk data across environments, with a preview
of everything that will change.

All of these operations are TOML profiles: versioned alongside your project and runnable
without a GUI.

```sh
hermes run profiles/sync-staging.toml
```

## Contributing

The project follows TDD with a minimum of 85% coverage, Conventional Commits, and both a code
review and a security review on every PR.

```sh
go test ./...                                        # full suite
go test -race -coverprofile=coverage.out ./internal/...
go tool cover -func=coverage.out                     # coverage (floor: 85%)
golangci-lint run                                    # Go lint
npm run lint                                         # frontend lint
```

Integration tests use testcontainers against PostgreSQL 13, 15, 16, 17, and 18 — you need
Docker available to run them.

## License

Open source. The final license (Apache-2.0) will be published in the `LICENSE` file before the
first release.
