# Targets used both locally and by CI, so that "green here" and "green there"
# mean the same thing. Run `make help` for the list.

GO ?= go
GOLANGCI_LINT ?= golangci-lint
COVERAGE_PROFILE ?= coverage.out
COVERAGE_MINIMUM ?= 85
INTEGRATION_TAGS ?= integration
GOVULNCHECK_VERSION ?= v1.7.0

# The race detector needs a C toolchain, which Windows does not ship. Install
# mingw-w64, or run `make test RACE=` to trade the detector for a green run.
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
	$(GO) run ./scripts/checkcoverage -profile=$(COVERAGE_PROFILE) -min=$(COVERAGE_MINIMUM)

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
# The pattern used to spell out the shapes a reference could take, and so missed
# docs/README.md, whose second segment is uppercase, and any mention in prose.
# The directory is not published at all, so naming it is the problem, in any form.
	@! grep -nE 'docs/|CLAUDE\.md|\.claude/' README.md || \
		(echo "README must not reference files that are not in the repository" && exit 1)

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
