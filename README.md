# Hermes

[![CI](https://github.com/gsoares85/hermes/actions/workflows/ci.yml/badge.svg)](https://github.com/gsoares85/hermes/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/gsoares85/hermes/branch/main/graph/badge.svg)](https://codecov.io/gh/gsoares85/hermes)
[![Release](https://img.shields.io/github/v/release/gsoares85/hermes?include_prereleases&sort=semver)](https://github.com/gsoares85/hermes/releases)
[![License](https://img.shields.io/github/license/gsoares85/hermes)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/gsoares85/hermes)](go.mod)
[![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macos%20%7C%20windows-blue)](#installation)

A **native, open source desktop database manager** for PostgreSQL, written in Go.

Fast, lightweight clients (TablePlus, Postico, Beekeeper) can't compare structure, compare
data, transfer between databases, or run backups. The tools that can (Navicat, DBeaver PRO,
dbForge) are heavy, expensive, or both. Hermes exists to fill that gap: the four operations
people buy a license for — **structure sync, data sync, data transfer, and integrated
backup/restore** — in a native app that is fast and free.

The bet is focus: do for **one** engine what the competition tries to do for twenty.

## Project status

**Pre-alpha — under active development.** You can connect to a server and see the databases you
have access to; there is no object tree, no SQL editor and no backup yet. This README grows with
every feature shipped: anything documented below with an example works. Anything in the
*Roadmap* section does not.

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

- **PostgreSQL 12 or newer** on the server you want to manage. Every change is
  tested against PostgreSQL 12, 13, 15, 16, 17 and 18.
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

Every release publishes binaries plus `SHA256SUMS` on the
[releases page](https://github.com/gsoares85/hermes/releases):

| Platform | Architecture | Artifact |
|---|---|---|
| Linux | x86-64 | `hermes-linux-amd64` |
| macOS | Apple Silicon | `hermes-darwin-arm64` |
| Windows | x86-64 | `hermes-windows-amd64` |

Each one ships alongside a `hermes-cli-*` binary of the same platform. Intel Macs and
arm64 Linux are not published yet — build from source there. Signed installers arrive
with version 1.0.

Check what you have:

```console
$ hermes --version
hermes v0.1.0 (a1b2c3d, 2026-08-30T12:00:00Z, linux/amd64)
```

### From source

Requires [Go 1.25+](https://go.dev/dl/), [Node.js 22.12+](https://nodejs.org/), and the
[Wails v3](https://wails.io/) CLI.

```sh
git clone https://github.com/gsoares85/hermes.git
cd hermes

make build   # frontend and every binary
make dev     # the desktop app, in development mode
make package # package for the current platform
```

`make help` lists every target.

## Features

### Connecting

Open Hermes and fill in the connection form: host, port, user, password. **The database name is
optional.** Leave it empty and Hermes connects to the server's maintenance database, then shows
you every database that user is actually allowed to open — so you can look before you know what
you are looking for.

```
Host       db.example.com
Port       5432
Database   (leave empty to see what is there)
User       reporting
Password   ••••••••
SSL mode   require
```

Press **Test connection** to check the settings without keeping anything open, or **Connect** to
open the connection. Once connected, the databases you can reach appear as a list; picking one
fills the database field.

Already have a connection string? Paste it and press **Fill the form**:

```
postgres://reporting@db.example.com:5432/analytics?sslmode=verify-full&sslrootcert=/etc/ssl/ca.pem
```

Every part is read into the form — host, port, database, user, SSL mode and certificate paths.
The password is deliberately **not** copied out of the URI, even when it carries one: Hermes
tells you a password was present and asks you to type it, rather than keeping the secret in the
window. A URI with no database at all works too:

```
postgres://reporting@db.example.com:5432
```

### Failures that tell you what to do

When a connection fails, Hermes does not show you the driver's message. It shows what happened,
why it probably happened, and what to do next:

```
The server has no rule allowing "reporting" to connect from this address.

pg_hba.conf decides who may connect from where, and it is consulted before the password is.
No rule matched this user, this database and this client address.

Add a pg_hba.conf line covering "reporting" from this address and reload the server.
This is a change on the server, not in these settings.
```

A wrong password, a missing `pg_hba` rule, a name that does not resolve, a closed port, a
firewall swallowing packets, a database that does not exist, a rejected certificate and a
dropped connection each get their own explanation, because each is fixed somewhere different.
The driver's original message is still there, one click away, for when you need it.

### TLS

All six libpq `sslmode` values work — `disable`, `allow`, `prefer`, `require`, `verify-ca` and
`verify-full` — along with client certificates:

```
postgres://reporting@db.example.com/analytics?sslmode=verify-full&sslrootcert=/etc/ssl/ca.pem&sslcert=/etc/ssl/client.pem&sslkey=/etc/ssl/client.key
```

`verify-ca` checks that the server's certificate was signed by the authority you supplied;
`verify-full` also checks that the host you typed matches the certificate. Hermes never quietly
falls back to a weaker mode than the one you asked for.

### Session parameters

Anything in a pasted URI that PostgreSQL understands as a session setting is carried through:

```
postgres://reporting@db.example.com/analytics?application_name=hermes&search_path=reporting,public
```

Connection settings such as `connect_timeout` or `require_auth` are carried too, and kept apart
from session settings — sending one as the other would quietly drop it.

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

Read [CONTRIBUTING.md](CONTRIBUTING.md) — it describes the whole process, including the
version label every pull request needs.

The short version: TDD with a minimum of 85% statement coverage, Conventional Commits, one
commit per phase, and a code review plus a security review on every pull request.

```sh
make lint              # Go and frontend linters
make test              # unit tests
make cover             # unit tests plus the 85% floor
make audit             # known vulnerabilities in both dependency trees
make test-integration  # PostgreSQL 12, 13, 15, 16, 17 and 18 (needs Docker)
```

Please report security vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE).
