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
its password in your system keychain, mark it as production or as read-only, and browse what is
on it — databases, schemas, tables, views and sequences, with an object's properties and its
DDL beside the tree. There is no SQL editor and no backup yet. This README grows with every
feature shipped: anything documented below with an example works. Anything in the *Roadmap*
section does not.

Reading a schema out of `pg_catalog` and writing it back as DDL is what the *DDL* tab shows,
and it is what the structure diff will be built on. The diff itself does not exist yet: it is
named under *Roadmap*, not under *Features*, and it moves when there is something you can run.

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
version = 2

[[connection]]
id = 'b7f0c6e1-1a4d-4f2f-9d6a-2a1c8e0b3f55'
name = 'production — read only'
host = 'db.example.com'
port = 5432
database = 'analytics'
user = 'reporting'
sslmode = 'verify-full'
sslrootcert = '/etc/ssl/ca.pem'
environment = 'prod'
read_only = true

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

`version` is read before anything else. This Hermes reads versions 1 and 2 and always writes 2,
so a file saved by an older build keeps working — it comes back with no environment and not
read-only, which is what a file with no field for either one means. A file from a newer Hermes
is refused with a message about the version rather than half-understood, because the file says
how you reach production and guessing at it is not an option.

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

### Marking production, and connecting read-only

A connection can say what it is — `dev`, `staging` or `prod` — and whether it is allowed to
write at all:

```
Environment  [ prod                                                    v ]
Read-only    [x] The server is asked to refuse every statement that writes.
```

The form shows the value that goes in the file; the window draws it in words. A connection
labelled `prod` marks the **window** and not a panel: a band across the top and a **PRODUCTION**
badge in a coloured header, both of which stay put while you move between the tree, an object's
properties and its DDL. The mark belongs to the connection rather than to the screen showing
it, so it looks the same everywhere the connection appears — in the list of saved connections,
beside the state of the open one, and above everything you do with it.

**Read-only is enforced by the server, not by the window.** A connection marked read-only is
opened with `default_transaction_read_only` on, so PostgreSQL is what refuses. Nothing Hermes
forgets to check can go around it, and nothing has to guess whether a statement writes by
reading the SQL — which is what any client-side version of this would have to do, and it would
be wrong about the first function that writes inside itself. Browsing to another database on
the same server opens a second connection, and it carries the mark with it. A connection that
sets the same parameter in its own session settings is refused rather than silently
overridden, because a protection you can argue with from two places is not one.

What the setting is not is a lock. It is the default of each transaction, so a session that
ran `SET default_transaction_read_only = off` would be writing again. Nothing in Hermes sends
arbitrary SQL today; the SQL editor, when it arrives, will have to refuse those statements on a
marked connection itself.

There is no SQL editor yet, so nothing in Hermes sends a write today: the gate is what that
editor will run into, and it is already proved against real servers rather than promised. The
test opens a marked connection, sends an `INSERT`, and requires both the server's refusal and
the explanation you would read:

```
This connection is marked read-only, so the server refused a statement that writes.

Hermes opens a connection marked read-only with default_transaction_read_only on, and the
server refuses every insert, update, delete and DDL inside it. The refusal comes from the
server, so nothing in this window went around it.

Clear the read-only mark on this connection and open it again, if writing to
db.example.com:5432 is what you meant to do.
```

A write refused on a connection you did **not** mark is a different message, because it is
fixed somewhere else: a standby accepts no writes, and a server, a database or a role can be
left with `default_transaction_read_only` on.

Both settings belong to the connection, so they go in the file with it and are still there
after a restart:

```toml
[[connection]]
id = 'b7f0c6e1-1a4d-4f2f-9d6a-2a1c8e0b3f55'
name = 'production — read only'
host = 'db.example.com'
port = 5432
user = 'reporting'
environment = 'prod'    # dev | staging | prod, omitted for a connection nobody labelled
read_only = true
```

### Browsing objects

Once a connection is open, its object tree appears: **server → databases → schemas → tables,
views and sequences.**

```
[-] analytics                 database
  [-] reporting               schema
        daily_revenue         table
        customer_lifetime     view
        invoice_id_seq        sequence
  [+] public                  schema
[+] postgres                  database
```

**Every level is read when you open it, and never before.** Expanding a schema asks one
question about that schema — names and kinds, no columns, no indexes, no constraints — so a
schema with five thousand tables opens as quickly as one with ten, and nothing is loaded on the
chance that you might look at it. The budget is a test rather than an intention: expanding a
schema of 5,000 tables in under a second, checked in CI against the oldest server in the
matrix, alongside a test that counts the queries a level costs so that "fast on my laptop"
cannot hide a query per object.

Expanding never freezes the window. The node that is loading says so, the rest of the tree
stays live, and collapsing a node stops the request instead of waiting for an answer nobody
wants. The rows are virtualised from the first line, so the window draws what fits on screen
whatever the level holds.

Type in **Filter by name** and the tree narrows as you type, highlighting the part that matched:

```
Filter by name  [ invoice        ]   [ ] System schemas
```

What is already on screen is filtered on every keystroke, so the box keeps up with typing. Once
the typing settles, the same text goes back to the server, so the objects of the schemas you
open next are narrowed there too — a level of fifty thousand names is never carried across just
to be thrown away here. Schemas themselves are never narrowed by it: hiding the schema that
holds the table you are looking for would hide the answer along with the noise.

PostgreSQL's own schemas — `pg_catalog`, `information_schema`, `pg_toast` — are hidden until you
tick **System schemas**, which re-reads that level rather than revealing something already
fetched. Names are shown exactly as they were created, accents, spaces, capitals and all: a
schema called `Relatórios Mensais` reads as `Relatórios Mensais`, without the quotes SQL would
need. A database your role may not open reports why on its own node, and the databases beside it
stay usable.

Selecting an object opens a panel beside the tree, with two tabs:

**Properties** — the columns with their types and what is declared on them, the constraints and
the indexes, and the facts that are not a column: that a table is unlogged, that it is a
partition, that row security is on.

```
daily_revenue
analytics · reporting

[ Properties ]  [ DDL ]

Column         Type      Null       Default
id             bigint    not null
captured_on    date      not null
gross_cents    bigint    not null   0
note           text

Constraints
  daily_revenue_pkey: PRIMARY KEY (id)
  daily_revenue_positive: CHECK ((gross_cents >= 0))

Indexes
  CREATE INDEX daily_revenue_captured_on_idx ON reporting.daily_revenue USING btree (captured_on)
```

**DDL** — the statements that would build the object, written by the same generator the
structure sync is built on:

```sql
-- Every name below is written bare, so this builds into whatever
-- schema the search path names. Change the line below to build elsewhere.
SET search_path TO "reporting";

CREATE TABLE "daily_revenue" (
    "id" bigint GENERATED ALWAYS AS IDENTITY NOT NULL,
    "captured_on" date NOT NULL,
    "gross_cents" bigint DEFAULT 0 NOT NULL,
    "note" text,
    CONSTRAINT "daily_revenue_pkey" PRIMARY KEY (id),
    CONSTRAINT "daily_revenue_positive" CHECK ((gross_cents >= 0))
);

CREATE INDEX daily_revenue_captured_on_idx ON reporting.daily_revenue USING btree (captured_on);
```

Every identifier is quoted and no statement names a schema, so the script builds the object
wherever the search path points — which is what lets the same text be read here and applied
somewhere else. It is the script the product would run, not a rendering of it made for reading:
a test requires this tab to be **identical** to what the generator produces for the same
object, rather than similar, so the screen can never quietly become a second DDL generator.

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

**Foundation** — a cancelable job engine with progress reporting. Connecting, actionable failure
diagnostics, keychain-backed passwords and lazy object-tree navigation are done.

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
