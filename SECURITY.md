# Security policy

Hermes holds credentials for production databases. Treat anything that touches
secrets, subprocesses or logs as security-relevant.

## Supported versions

The project is pre-1.0: only the latest release receives fixes. Once 1.0 ships,
this section will name the supported line explicitly.

## Reporting a vulnerability

Report privately through GitHub: open the repository's **Security** tab and use
**Report a vulnerability**. Please do not open a public issue for a
vulnerability, and do not include a working exploit in the first report.

Useful in a report: what an attacker gains, the smallest reproduction you have,
the affected version or commit, and your platform. You can expect an
acknowledgement within a few days and a fix or a plan before any public
disclosure.

## What the project promises

These are commitments, so a violation of any of them is a vulnerability worth
reporting:

- **Secrets never touch disk in plain text.** Passwords live in the operating
  system keychain — Keychain on macOS, DPAPI on Windows, Secret Service on
  Linux. Connection files are meant to be committed to a repository and contain
  no credentials.
- **Secrets never appear in `argv`.** Subprocesses such as `pg_dump` receive
  credentials through the environment or through a temporary `.pgpass` created
  with mode 0600 and removed afterwards, including when the process is killed.
- **Secrets never appear in logs.** Log output, exported reports and error
  messages are redacted.
- **No network calls beyond your databases.** No telemetry, no update pings and
  no AI services unless you turn them on yourself.
- **No credential crosses into the frontend.** The interface receives connection
  state, never a password.

## Release pipeline

Signing keys and their passwords exist only as CI secrets. The workflow that
signs and publishes runs separately from the workflow that builds pull requests,
so a pull request from a fork never has access to them. Every release publishes
checksums.

## Scope

In scope: credential handling, subprocess execution, log redaction, the update
mechanism, and anything that lets a connection marked read-only or production
perform a write it should not.

Out of scope: vulnerabilities in PostgreSQL itself, in the PostgreSQL client
tools, or attacks that require an already-compromised operating system account.
