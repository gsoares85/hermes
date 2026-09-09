# Hermes

<!--
  Static badges on purpose. The badge services are anonymous, so while this
  repository is private every badge that queries it renders as an error rather
  than as a value: the Actions workflow badge answers 404, and shields.io
  answers "repo not found". The CI and coverage badges are not replaced with
  static ones, because a fixed "passing" would be a claim about a state nobody
  can check. Restore the live badges in the change that makes the repository
  public.
-->

[![Status](https://img.shields.io/badge/status-pre--alpha-orange)](#project-status)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8)](go.mod)
[![PostgreSQL](https://img.shields.io/badge/postgresql-12%2B-336791)](#requirements)
[![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macos%20%7C%20windows-blue)](#installation)

A **native, open source desktop database manager** for PostgreSQL, written in Go.

Fast, lightweight clients (TablePlus, Postico, Beekeeper) can't compare structure, compare
data, transfer between databases, or run backups. The tools that can (Navicat, DBeaver PRO,
dbForge) are heavy, expensive, or both. Hermes exists to fill that gap: the four operations
people buy a license for — **structure sync, data sync, data transfer, and integrated
backup/restore** — in a native app that is fast and free.

The bet is focus: do for **one** engine what the competition tries to do for twenty.

## Project status

**Pre-alpha — under active development.** You can connect to a server, save the connection with
its password in your system keychain, and see the databases you have access to; there is no
object tree, no SQL editor and no backup yet. This README grows with every feature shipped:
anything documented below with an example works. Anything in the *Roadmap* section does not.

Work is under way on reading a schema out of `pg_catalog` and writing it back as DDL, which is
what the structure diff will be built on. It has no command and no screen yet, so there is
nothing here to show you: it is named under *Roadmap*, not under *Features*, and it moves when
there is something you can run.

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

The desktop app shows the same version in the status bar at the bottom of the window, with the
commit and the build date in its tooltip — so a bug report can always say which build it is
about.

### From source

Requires [Go 1.25+](https://go.dev/dl/), [Node.js 22.13+](https://nodejs.org/), and the
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

While Hermes is working, a **Stop** button appears beside the others and is the only one that
stays live. Every long step on this screen ends either in your keychain or in a server, and both
can take as long as they like: reading a password on macOS or Linux can raise a dialog that
waits for you, and a host that is not answering runs to the driver's own timeout. Stopping is
never a failure — Hermes says the operation was stopped, and where the result is genuinely
unknown, such as a save cut off part way, it says that instead of claiming either outcome.

```
[ Test connection ]  [ Connect ]  [ Save connection ]              [ Stop ]
Working…
```

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

### Saving a connection

Give the connection a name and press **Save connection**. It appears in the *Saved connections*
list at the top of the form; clicking one loads it back, and **Forget** removes it along with
its password.

The settings go in a file you can read, version and copy between machines:

| Platform | Connections file |
|---|---|
| Linux | `~/.config/hermes/connections.toml` |
| macOS | `~/Library/Application Support/hermes/connections.toml` |
| Windows | `%AppData%\hermes\connections.toml` |

```toml
version = 1

[[connection]]
id = 'b7f0c6e1-1a4d-4f2f-9d6a-2a1c8e0b3f55'
name = 'production — read only'
host = 'db.example.com'
port = 5432
database = 'analytics'
user = 'reporting'
sslmode = 'verify-full'
sslrootcert = '/etc/ssl/ca.pem'

[connection.params]
application_name = 'hermes'

[[connection]]
id = '3c9a1e42-7b58-4d10-9f2e-6d4b0a7c1e93'
name = 'local'
host = 'localhost'
port = 5432
user = 'postgres'
sslmode = 'disable'
```

**There is no password field in this format, and no way to put one in it.** A file that names
`password` at the top level is refused with an error saying so, rather than read as if the
password had taken effect. The same goes for the two free-form tables: `password`,
`sslpassword` and `pgpassword` are refused under `[connection.params]` and
`[connection.options]` too, on the way in and on the way out, because a key libpq would read a
secret from is a secret in a plain-text file whatever table it sits in. Committing this file to
a repository or mailing it to a colleague hands over no secret.

The `id` is what the password is filed under in your keychain, which is why it is a random
identifier rather than the name: renaming a connection, or moving it to a different host, keeps
the password attached to it.

`version = 1` is read before anything else. A file from a newer Hermes is refused with a clear
message instead of being half-understood — the file says how you reach production, and guessing
at it is not an option.

### Where your password is kept

In the password store your operating system already has:

| Platform | Store |
|---|---|
| Linux | the Secret Service of your desktop session — GNOME Keyring, KWallet |
| macOS | the login keychain |
| Windows | the Credential Manager, encrypted with DPAPI |

Hermes talks to each of them through its native interface, never by shelling out to a
command-line tool. It reads a password only when it actually opens the connection, so listing
your saved connections never triggers the authorization dialog that macOS and Linux can raise.

Four things Hermes never does with your password, each one covered by a test that fails the
build:

- write it to disk in plain text — the keychain is the only place it is ever stored;
- put it in the connections file, which has nowhere to put one;
- print it in a log, an error message or a report;
- pass it on the command line of a program it starts, where every other process on the machine
  could read it. The route `pg_dump` and `pg_restore` will take is already built and tested: the
  environment, or a temporary password file created `0600` and removed afterwards. A file left
  behind by a crash, a kill or a power cut is removed by the next start-up, which asks the
  operating system whether anything still holds the file rather than trusting what its name
  says — so a Hermes running a backup keeps its file, and one that died does not.

Anything that could carry a connection string is redacted on the way out, so a message you paste
into a bug report reads:

```
failed to connect to postgres://reporting:xxxxx@db.example.com:5432/analytics?sslmode=verify-full
```

#### A Linux without a keyring

Minimal desktops, containers and some window managers run no Secret Service. Hermes says so
rather than failing quietly, and keeps working:

```
Hermes could not reach the Secret Service of this desktop session: the vault is unavailable:
no session bus. Passwords are kept in memory for this session only, and will be asked for
again the next time Hermes starts. Install and start a keyring — gnome-keyring or
kwalletmanager — and start Hermes again.
```

Your connections are still saved; only the passwords are forgotten when you quit. Nothing is
written to disk to work around it — an encrypted file whose key sits next to it is plain text
with extra steps. To get persistent passwords, install a keyring:

```sh
# Debian / Ubuntu
sudo apt install gnome-keyring

# Fedora
sudo dnf install gnome-keyring

# KDE
sudo apt install kwalletmanager
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

Neither table will hold a password. `password`, `sslpassword` and `pgpassword` are refused under
`[connection.params]` and `[connection.options]`, on the way in and on the way out, with an
error saying where passwords are actually kept:

```toml
[[connection]]
id = 'b7f0c6e1-1a4d-4f2f-9d6a-2a1c8e0b3f55'
host = 'db.example.com'
port = 5432
user = 'reporting'

[connection.options]
password = 'hunter2'
```

```
~/.config/hermes/connections.toml: line 3, options.password: invalid connection:
options.password would put a password in the connections file in plain text — Hermes keeps
passwords in the keychain of the system
```

## Roadmap

What is being built, in order:

**Foundation** — lazy object-tree navigation and a cancelable job engine with progress
reporting. Connecting, actionable failure diagnostics and keychain-backed passwords are done.

**Query and data** — SQL editor with cancelable execution, a virtualized grid using keyset
pagination (no `OFFSET` on large tables), and inline editing that shows the `UPDATE` before it
runs.

**Backup and restore** — wizards over `pg_dump` and `pg_restore`, with selective object
restore and a local catalog of the backups you generate.

**Structure sync** — native schema diff over `pg_catalog`, producing a reviewable migration
script, a difference report, and a CI mode that turns the diff into a pipeline gate. Reading a
schema and writing it back as DDL is in place underneath, tested against PostgreSQL 12 through
18 by building a schema, reading it, writing it out, applying it to an empty schema and
comparing the two models.

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
make bench             # the performance budgets (needs Docker)
```

Please report security vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE).
