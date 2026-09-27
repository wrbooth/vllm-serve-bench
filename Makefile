# vllm-serve-bench — build / test / lint targets. `make help` lists them.
#
# Every lint tool's version is pinned HERE and nowhere else; CI calls the same
# targets (`make install-tools`, `make lint`) so a developer and CI cannot
# disagree about which linter said what.

BIN := bin/bench
GOPATH_BIN := $(shell go env GOPATH)/bin

GOLANGCI_VERSION := v2.12.2
ACTIONLINT_VERSION := v1.7.12
RUFF_VERSION := 0.16.9
MARKDOWNLINT_VERSION := 0.23.3

# `go install` puts tools in $(GOPATH)/bin, which is often not on PATH. Calling
# a linter bare then fails with the shell's "command not found", which reads as
# "not installed" — and `go vet` is NOT a substitute for the suite below. Find
# the binary where it actually lives, and say what to do if it is absent.
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || command -v $(GOPATH_BIN)/golangci-lint 2>/dev/null)
ACTIONLINT := $(shell command -v actionlint 2>/dev/null || command -v $(GOPATH_BIN)/actionlint 2>/dev/null)

export GOLANGCI_LINT_CACHE := $(CURDIR)/.golangci-cache

define REQUIRE
	@if [ -z "$(1)" ]; then \
		echo "$(2) was not found on PATH or in $(GOPATH_BIN)."; \
		echo "  install it:  make install-tools"; \
		exit 1; \
	fi
endef

PY_FILES := $(shell git ls-files '*.py' 2>/dev/null)

.PHONY: build test race cover test-gpu fmt vet lint lint-go lint-actions lint-py lint-md install-tools tool-versions clean help

build: ## Build the bench binary into bin/
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/bench

test: ## Run all Go tests
	go test ./...

race: ## Run all Go tests with the race detector
	go test -race ./...

# Coverage floor for the packages whose output IS the result: metric math, SSE
# parsing, prompt generation, report aggregation. A wrong percentile there is a
# wrong slide. cmd/ and deploy glue are exercised by the fake-server
# integration tests instead and are not held to the number.
COVER_MIN := 85
COVER_PKGS := ./internal/metrics/... ./internal/openai/... ./internal/prompts/... ./internal/report/...

cover: ## Coverage on the result-producing packages; fails under COVER_MIN
	@pkgs=$$(for p in $(COVER_PKGS); do d=$${p%/...}; [ -d "$$d" ] && echo $$p; done); \
	if [ -z "$$pkgs" ]; then echo "cover: no result packages yet, skipping"; exit 0; fi; \
	go test -count=1 -coverprofile=coverage.out $$pkgs >/dev/null && \
	total=$$(go tool cover -func=coverage.out | awk '/^total:/ {sub("%","",$$3); print $$3}'); \
	echo "coverage $$total% (floor $(COVER_MIN)%)"; \
	awk -v t="$$total" -v m="$(COVER_MIN)" 'BEGIN { exit (t+0 < m+0) }' || \
	{ echo "cover: below the $(COVER_MIN)% floor"; exit 1; }

test-gpu: ## Tests tagged `gpu` against a live engine (needs VLLM_BASE_URL); never run in CI
	@if [ -z "$$VLLM_BASE_URL" ]; then echo "test-gpu: set VLLM_BASE_URL (e.g. http://localhost:8000)"; exit 1; fi
	go test -count=1 -tags gpu ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format Go (gofumpt + goimports) and Python (ruff) sources
	$(call REQUIRE,$(GOLANGCI),golangci-lint)
	$(GOLANGCI) fmt ./...
	@if [ -n "$(PY_FILES)" ]; then uvx ruff@$(RUFF_VERSION) format $(PY_FILES); fi

lint: lint-go lint-actions lint-py lint-md ## Run every linter CI runs

lint-go: ## golangci-lint, strict suite (see .golangci.yml)
	$(call REQUIRE,$(GOLANGCI),golangci-lint)
	$(GOLANGCI) run ./...

lint-actions: ## actionlint on .github/workflows (shellcheck-aware when installed)
	$(call REQUIRE,$(ACTIONLINT),actionlint)
	$(ACTIONLINT)

lint-py: ## ruff check + format check (skipped when the repo has no Python yet)
	@if [ -z "$(PY_FILES)" ]; then echo "lint-py: no Python files, skipping"; else \
		uvx ruff@$(RUFF_VERSION) check $(PY_FILES) && \
		uvx ruff@$(RUFF_VERSION) format --check $(PY_FILES); fi

lint-md: ## markdownlint on docs, wiki and top-level markdown
	npx --yes markdownlint-cli2@$(MARKDOWNLINT_VERSION)

install-tools: ## Install the pinned Go-based linters (honours GOBIN)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

tool-versions: ## Print the pinned tool versions
	@echo "golangci-lint $(GOLANGCI_VERSION)"
	@echo "actionlint    $(ACTIONLINT_VERSION)"
	@echo "ruff          $(RUFF_VERSION)"
	@echo "markdownlint  $(MARKDOWNLINT_VERSION)"

clean: ## Remove build output and the lint cache
	rm -rf bin .golangci-cache

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
