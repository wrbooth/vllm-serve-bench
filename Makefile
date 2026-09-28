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
GITLEAKS_VERSION := v8.30.1
# v0.8.0 requires Go 1.26; this repo builds with 1.25 and CI's setup-go does not
# fetch a newer toolchain, so stay on the last release that builds with it.
KUBECONFORM_VERSION := v0.7.0

# `go install` puts tools in $(GOPATH)/bin, which is often not on PATH. Calling
# a linter bare then fails with the shell's "command not found", which reads as
# "not installed" — and `go vet` is NOT a substitute for the suite below. Find
# the binary where it actually lives, and say what to do if it is absent.
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || command -v $(GOPATH_BIN)/golangci-lint 2>/dev/null)
ACTIONLINT := $(shell command -v actionlint 2>/dev/null || command -v $(GOPATH_BIN)/actionlint 2>/dev/null)
GITLEAKS := $(shell command -v gitleaks 2>/dev/null || command -v $(GOPATH_BIN)/gitleaks 2>/dev/null)
KUBECONFORM := $(shell command -v kubeconform 2>/dev/null || command -v $(GOPATH_BIN)/kubeconform 2>/dev/null)

# Owner-private files live in the PRIMARY checkout only (worktrees do not get
# gitignored files), so resolve it from git's common dir.
PRIMARY := $(shell dirname "$$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)")
DENYLIST := $(PRIMARY)/.private/denylist

export GOLANGCI_LINT_CACHE := $(CURDIR)/.golangci-cache

define REQUIRE
	@if [ -z "$(1)" ]; then \
		echo "$(2) was not found on PATH or in $(GOPATH_BIN)."; \
		echo "  install it:  make install-tools"; \
		exit 1; \
	fi
endef

PY_FILES := $(shell git ls-files '*.py' 2>/dev/null)

.PHONY: build test race cover test-gpu fmt vet lint lint-go lint-actions lint-py lint-md lint-secrets lint-private lint-deploy lint-k8s lint-compose install-tools install-kubeconform tool-versions timesheet clean help

build: ## Build the bench binary into bin/
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/bench

test: ## Run all Go tests
	go test ./...

race: ## Run all Go tests with the race detector
	go test -race ./...

# Coverage floor for the packages whose output IS the result: metric math, SSE
# parsing, prompt generation, the load generator's windowing, the run-directory
# writer, report aggregation, the cross-check against vLLM's client. A wrong percentile there is a wrong slide. cmd/ and deploy glue are exercised by the fake-server
# integration tests instead and are not held to the number.
COVER_MIN := 85
COVER_PKGS := ./internal/metrics/... ./internal/crosscheck/... ./internal/openai/... ./internal/prompts/... ./internal/loadgen/... ./internal/results/... ./internal/report/... ./internal/sampler/...

cover: ## Coverage on the result-producing packages; fails under COVER_MIN
	@pkgs=$$(for p in $(COVER_PKGS); do d=$${p%/...}; [ -d "$$d" ] && echo $$p; done); \
	if [ -z "$$pkgs" ]; then echo "cover: no result packages yet, skipping"; exit 0; fi; \
	go test -count=1 -coverprofile=coverage.out $$pkgs >coverage.log 2>&1 || \
		{ cat coverage.log; echo "cover: tests failed (above), so no coverage figure"; exit 1; }; \
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

lint: lint-go lint-actions lint-py lint-md lint-secrets lint-private ## Run every linter (lint-private is local-only)

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

lint-secrets: ## gitleaks over the full git history (tokens, keys)
	$(call REQUIRE,$(GITLEAKS),gitleaks)
	$(GITLEAKS) git --no-banner --redact .

lint-private: ## Tracked files vs the owner's private denylist (names, LAN addresses); skips where the list is absent (CI)
	@if [ ! -f "$(DENYLIST)" ]; then echo "lint-private: no $(DENYLIST), skipping"; exit 0; fi; \
	if git grep -n -I -i -w -F -f "$(DENYLIST)" -- . ':!raw/'; then \
		echo "lint-private: tracked content matches the private denylist (above). This repo is public."; exit 1; \
	else echo "lint-private: clean"; fi

# Deploy manifests are linted by their own targets, NOT by `make lint`:
# kubeconform downloads the Kubernetes JSON schemas on every run (network), and
# `docker compose config` needs the Docker CLI with the Compose plugin. `make
# lint` stays offline and Docker-free; CI runs `make lint-deploy` as its own job.
lint-deploy: lint-k8s lint-compose ## Validate deploy/: kubeconform + docker compose config (network, Docker CLI)

lint-k8s: ## kubeconform -strict on deploy/k8s against the default schema location (downloads schemas)
	$(call REQUIRE,$(KUBECONFORM),kubeconform)
	$(KUBECONFORM) -strict -summary deploy/k8s

# The committed .env plus each engine config, with dummy values for what
# engine.sh (MODEL) and the gitignored .env.local (HF_CACHE) would supply.
# Shell environment overrides env files in Compose, so these never shadow a
# committed value. The bench profile is enabled so its service is checked too.
lint-compose: ## docker compose config -q for every engine config (needs the Docker CLI, not the daemon)
	@command -v docker >/dev/null || { echo "lint-compose: docker CLI not found"; exit 1; }
	@cd deploy/compose && for f in engine/*.env; do \
		echo "compose config: $$f"; \
		MODEL=dummy/model HF_CACHE=/nonexistent/hf-cache MODELS_DIR=/nonexistent/models \
			docker compose --env-file .env --env-file "$$f" --profile bench config -q || exit 1; \
	done

install-tools: install-kubeconform ## Install the pinned Go-based linters (honours GOBIN)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)
	go install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)

install-kubeconform: ## Install only the pinned kubeconform (the CI deploy job needs nothing else)
	go install github.com/yannh/kubeconform/cmd/kubeconform@$(KUBECONFORM_VERSION)

tool-versions: ## Print the pinned tool versions
	@echo "golangci-lint $(GOLANGCI_VERSION)"
	@echo "actionlint    $(ACTIONLINT_VERSION)"
	@echo "ruff          $(RUFF_VERSION)"
	@echo "markdownlint  $(MARKDOWNLINT_VERSION)"
	@echo "gitleaks      $(GITLEAKS_VERSION)"
	@echo "kubeconform   $(KUBECONFORM_VERSION)"

# Transcripts of conversations started outside this repo (machine-specific, so
# kept in the primary checkout's gitignored .env.local as TIMESHEET_ALSO=<globs>).
TIMESHEET_ALSO ?= $(shell sed -n 's/^TIMESHEET_ALSO=//p' "$(PRIMARY)/.env.local" 2>/dev/null)

timesheet: ## Worklog session table from Claude Code transcripts + commits (GAP=5m; TIMESHEET_ALSO in .env.local)
	@go run ./cmd/timesheet -gap $(or $(GAP),5m) -also '$(TIMESHEET_ALSO)'

clean: ## Remove build output and the lint cache
	rm -rf bin .golangci-cache

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
