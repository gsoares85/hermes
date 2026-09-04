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
# that is in fact tested. internal/vault is out for the same reason and by the
# same argument: only one of its three implementations compiles on any given
# machine, and all three are exercised by the keychain job, which runs on three
# runners and feeds no profile. The conformance suite of the vault is test code
# that had to be an ordinary package, because Go cannot share a helper written
# in a _test.go file with another package's tests, and more than half of it is
# the branch that reports a failure.
# Every run prints what was ignored, and ignoring everything is refused.
COVERAGE_IGNORE ?= github.com/gsoares85/hermes/internal/driver/postgres,github.com/gsoares85/hermes/internal/vault,github.com/gsoares85/hermes/internal/core/secret/secrettest
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
test: ## Run the unit tests
	$(GO) test $(RACE) ./...

.PHONY: test-integration
test-integration: ## Run the integration tests (requires Docker)
	$(GO) test $(RACE) -tags=$(INTEGRATION_TAGS) -timeout=20m ./...

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
