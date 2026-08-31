# Contributing to Hermes

Thanks for considering it. This document is the whole process — there is nothing
else to read before your first pull request.

## Getting set up

You need [Go 1.25+](https://go.dev/dl/), [Node.js 22.12+](https://nodejs.org/),
[Docker](https://docs.docker.com/get-docker/) for the integration tests, and the
[Wails v3](https://wails.io/) CLI for the desktop app.

```sh
git clone https://github.com/gsoares85/hermes.git
cd hermes

make build      # frontend + every Go binary
make test       # unit tests
make lint       # Go and frontend linters
make dev        # the desktop app, in development mode
```

`make help` lists every target. CI runs the same targets, so a green `make lint
test cover` locally means a green pipeline.

On Windows the race detector needs a C toolchain (mingw-w64). Without it, `make
test`, `make cover` and `make test-integration` all fail to build with
`cgo: C compiler "gcc" not found`. Either install mingw-w64, or pass `RACE=` to
the target you are running:

```sh
make cover RACE=
```

CI runs on Linux with the detector enabled, so a race is still caught there.

## How the work is organised

Every change is a branch off `main`, one pull request, and one release.

- **Branches** are named `feat/TASK-0001-short-slug`, `fix/...`, `refactor/...`.
- **Commits** follow [Conventional Commits](https://www.conventionalcommits.org/):
  an imperative English subject of at most 72 characters, and a body that
  explains what changed and why. The work is split into phases, and each phase
  is one commit — no `wip` commits, no single commit at the end. CI checks the
  type, the length and the author of every commit.
- **Pull requests are merged with a merge commit or a rebase, never squashed.**
  One commit per phase is the point, and a squash throws that history away. It
  would also replace the subjects with the title of the pull request, which is
  what the release reads when no version label is present.
- **Pull requests** describe everything inline: the problem, the change, and how
  to test it. A reviewer should not need to open anything else.

### Every pull request needs a version label

Merging to `main` publishes a release, and the size of the version bump comes
from a label on the pull request:

| Label | When |
|---|---|
| `release:patch` | internal plumbing, a fix, or infrastructure work |
| `release:minor` | new capability visible to whoever uses the product |
| `release:major` | 1.0, and after it any break in the profile format or the CLI |

Without a label the release workflow refuses to guess and fails.

## Definition of done

A change is ready when all of this is true:

1. **Tests came first.** New behaviour starts with a failing test; a bug fix
   starts with a test that reproduces the bug.
2. **Coverage is at least 85%** of statements under `internal/`. CI fails below
   that, and a profile that measures nothing counts as a failure, not a pass.
3. **Nothing destructive runs without a preview.** Any operation that changes
   structure or data must show the SQL it will run, and let the user edit it.
4. **No secret is written in plain text or passed in `argv`.** Credentials live
   in the OS keychain; subprocesses get them through the environment or a
   temporary `.pgpass` with mode 0600 that is removed afterwards.
5. **Nothing blocks the interface.** Long operations are cancellable jobs that
   report progress.
6. **The README is updated** as the last step, with a usage example for anything
   new that people can see.

## Testing

```sh
make test              # unit tests, no Docker needed
make cover             # unit tests plus the coverage floor
make test-integration  # integration tests (starts PostgreSQL containers)
```

Integration tests run against PostgreSQL 12, 13, 15, 16, 17 and 18. Anything
that reads the catalog or generates DDL has to pass on all six. 12 is the
oldest version Hermes supports, so it is tested rather than assumed.

Two properties are treated as non-negotiable and are covered by tests: comparing
a schema against itself must report zero differences, and generating DDL from
the model, applying it, and reading it back must produce the same model.

## Authorship

Commits are authored by the people who write them. Attribution to an AI
assistant is not accepted anywhere in the history — not in a commit message, a
trailer, a branch name, or a pull request description, and not in the author or
committer identity of the commit either. CI enforces this and will fail the
build.

The rule is about attribution, not vocabulary: a commit that mentions a tool or a
file by name is fine.

## Reporting problems

Bugs and feature requests go to [issues](https://github.com/gsoares85/hermes/issues).
Security vulnerabilities do not — see [SECURITY.md](SECURITY.md).
