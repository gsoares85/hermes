# Targets used both locally and by CI, so that "green here" and "green there"
# mean the same thing. Run `make help` for the list.

GO ?= go
GOLANGCI_LINT ?= golangci-lint
COVERAGE_PROFILE ?= coverage.out
COVERAGE_MINIMUM ?= 85

# Left out of the coverage count, not out of testing. This list is the one the
# CI job uses, and the two have to stay identical: they diverged once, and the
# result was `make cover` failing on Linux while CI passed, which teaches people
# to distrust the local command rather than the code.
#
# The pgx adapter is behind the thin interfaces of internal/driver and is
# exercised by the integration suite, which `make cover` does not run: counting
# its statements while ignoring its coverage would drag the number down for code
# that is in fact tested. internal/vault/system is out for the same reason and
# by the same argument: only one of its implementations compiles on any given
# machine, and all of them are exercised by the keychain job, which runs on
# three runners and feeds no profile. It is the subpackage and not internal/vault
# — excluding the whole tree also excused the choosing, the fallback and the
# deferred opening, which are platform-independent, have a suite of their own
# and hold the concurrency worth counting. The conformance suites of the vault
# and of the job history are test code that had to be ordinary packages, because
# Go cannot share a helper written in a _test.go file with another package's
# tests, and more than half of each is the branch that reports a failure — which
# only runs when an implementation is broken.
# Every run prints what was ignored, and ignoring everything is refused.
COVERAGE_IGNORE ?= github.com/gsoares85/hermes/internal/driver/postgres,github.com/gsoares85/hermes/internal/vault/system,github.com/gsoares85/hermes/internal/core/secret/secrettest,github.com/gsoares85/hermes/internal/core/store/storetest
INTEGRATION_TAGS ?= integration
GOVULNCHECK_VERSION ?= v1.7.0

# The race detector needs a C toolchain, which Windows does not ship. Install
# mingw-w64, or pass RACE= to trade the detector for a green run. It applies to
# every target that runs tests, not just test: `make cover RACE=`,
# `make test-integration RACE=`. CI runs on Linux, where the detector is on.
RACE ?= -race

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-22s %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## Sync go.mod and go.sum
	$(GO) mod tidy

.PHONY: lint
lint: lint-go lint-frontend ## Run every linter

.PHONY: lint-go
lint-go: ## Run the Go linters
	$(GOLANGCI_LINT) run

.PHONY: lint-frontend
lint-frontend: ## Run the frontend linters
	cd frontend && npm run lint

.PHONY: test
test: test-go test-frontend ## Run the unit tests

.PHONY: test-go
test-go: ## Run the Go unit tests
	$(GO) test $(RACE) ./...

# The window is drawn from logic that is deliberately pure — which rows are
# visible, what a level may do next — and none of it was covered until a runner
# existed to cover it. It runs in node rather than in a browser environment for
# the same reason: a test that had to mount a component to ask whether a
# collapsed node may be asked for again would be testing React.
.PHONY: test-frontend
test-frontend: ## Run the frontend unit tests
	cd frontend && npm test

# How many test binaries may talk to Docker at once. Unset means one per core,
# which is what go test does on its own, and is the normal way to run this.
#
# It exists because of one failure worth recognising rather than debugging
# again. Five packages need containers — internal/core/catalog,
# internal/core/ddl, internal/core/conn, internal/driver/postgres and
# internal/testsupport — and each opens a Docker client the moment it starts. If
# the daemon refuses one of those connections, testcontainers walks its list of
# ways to find a Docker host, fails at all of them, and reports the error of the
# last one it tried: on Windows that is "rootless Docker is not supported on
# Windows", which has nothing to do with what happened. It then caches the
# failure for the life of the process, so a single refused connection at startup
# fails every test in that binary while the other packages pass.
#
# The message names the wrong thing, so read it as "the Docker daemon did not
# answer" and check that Docker is up and settled. If it keeps happening,
# `make test-integration INTEGRATION_PARALLEL=2` starts fewer binaries at once.
# It is not the default because the cause has not been pinned to concurrency —
# six consecutive full runs at one binary per core were clean — and bounding it
# costs about sixty per cent more wall clock for a guess.
INTEGRATION_PARALLEL ?=

.PHONY: test-integration
test-integration: ## Run the integration tests (requires Docker)
	$(GO) test $(RACE) -tags=$(INTEGRATION_TAGS) \
		$(if $(INTEGRATION_PARALLEL),-p $(INTEGRATION_PARALLEL)) -timeout=20m ./...

# How many times each benchmark repeats. Once is enough for a budget: what is
# being asked is "does a thousand tables read in under five seconds", not "what
# is the mean to three digits", and the fixture costs seconds to build.
BENCHTIME ?= 1x

# Without -race, and that is not an oversight. The detector multiplies the time
# of everything it watches, so a budget measured under it would be measuring the
# detector — and the number the product has to meet is the one a user gets,
# which is the build they run.
#
# -run with a pattern that matches nothing keeps the tests out: they share these
# packages, and running the whole suite again to reach the benchmarks would make
# this the slowest target for no reason.
.PHONY: bench
bench: ## Run the performance budgets (requires Docker)
	$(GO) test -tags=$(INTEGRATION_TAGS) -run='^$$' -bench=. -benchtime=$(BENCHTIME) \
		-timeout=20m ./internal/...

.PHONY: cover
cover: ## Run the unit tests and enforce the coverage floor
	$(GO) test $(RACE) -coverprofile=$(COVERAGE_PROFILE) -coverpkg=./internal/... ./internal/...
	$(GO) run ./scripts/checkcoverage -profile=$(COVERAGE_PROFILE) -min=$(COVERAGE_MINIMUM) -ignore="$(COVERAGE_IGNORE)"

.PHONY: cover-html
cover-html: cover ## Open the coverage report
	$(GO) tool cover -html=$(COVERAGE_PROFILE)

.PHONY: audit
audit: ## Scan both dependency trees for known vulnerabilities
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	cd frontend && npm audit --audit-level=high

.PHONY: check-commits
check-commits: ## Fail if a commit or the branch credits an AI assistant
	$(GO) run ./scripts/checkcommits -range=origin/main..HEAD -branch=$$(git rev-parse --abbrev-ref HEAD)

.PHONY: check-readme
check-readme: ## Fail if the README points at files that are not published
# A Go program rather than a grep, like the other two gates. A recipe runs on
# whatever shell the platform hands make, and the one Windows hands it does not
# understand `! cmd || (...)`: this target used to fail there with `"!" is not
# recognized`, on a machine where nothing was wrong with the README at all.
	$(GO) run ./scripts/checkreadme -file=README.md

.PHONY: build
build: build-frontend ## Build every binary
	$(GO) build ./...

.PHONY: build-frontend
build-frontend: ## Build the embedded frontend
	cd frontend && npm ci --ignore-scripts && npm run build

.PHONY: dev
dev: ## Run the desktop app in development mode
	wails3 task dev

.PHONY: package
package: ## Package the desktop app for the current platform
	wails3 task package

.PHONY: clean
clean: ## Remove build and coverage output
	$(GO) clean
	rm -f $(COVERAGE_PROFILE)
	cd frontend && npm run clean
