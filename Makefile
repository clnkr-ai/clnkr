VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LATEST_TAG ?= $(shell git tag -l '[0-9]*' --sort=-v:refname | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
HUGO ?= $(or $(shell command -v hugo 2>/dev/null),$(shell go env GOPATH)/bin/hugo)
PANDOC ?= pandoc
CLANKERVAL_PINNED_VERSION := 0.4.3
CLANKERVAL_BINARY ?= $(CURDIR)/clnkr
CLANKERVAL_PREFLIGHT = \
	clankerval_path="$$(command -v clankerval 2>/dev/null || true)"; \
	if [ -z "$$clankerval_path" ]; then \
		echo "error: expected clankerval $(CLANKERVAL_PINNED_VERSION) in PATH." >&2; \
		exit 1; \
	fi; \
	if ! version_output="$$("$$clankerval_path" --version 2>&1)"; then \
		echo "error: clankerval --version failed: $$version_output" >&2; \
		exit 1; \
	fi; \
	if [ "$$version_output" != "clankerval $(CLANKERVAL_PINNED_VERSION)" ]; then \
		echo "error: expected 'clankerval $(CLANKERVAL_PINNED_VERSION)' from 'clankerval --version', got '$$version_output'" >&2; \
		echo "download it from https://github.com/clnkr-ai/clankerval/releases" >&2; \
		exit 1; \
	fi; \

.DEFAULT_GOAL := build
.PHONY: \
	build clean install run \
	check test evaluations evaluations-live evaluations-live-openai evaluations-live-anthropic \
	help man docs docs-serve \
	_build-clnkr \
	_fmt _fmt-check _vet _lint _arch sloc frontend-sloc _workflow-make-targets \
	_hooks _check-docs _require-pandoc _site-sync _site-build

PREFIX ?= /usr/local
CORE_SLOC_LIMIT := 1300
FRONTEND_SLOC_LIMIT := 1000
DOC_MAN_DIR := build/docs/man
DOC_MAN_OUTPUTS := $(DOC_MAN_DIR)/clnkr.1
DOC_CONTENT_DIR := site/content/docs
DOC_PAGE_TEMPLATE := site/pandoc/doc-page.md
GENERATED_SITE_DOCS := \
	$(DOC_CONTENT_DIR)/clnkr.md

##@ Build
build: _build-clnkr ## Build shipped binaries

clean: ## Remove build artifacts
	rm -f clnkr
	rm -rf build/docs
	find "$(DOC_CONTENT_DIR)" -maxdepth 1 -type f ! -name '_index.md' -delete

install: build ## Install shipped binary
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 clnkr $(DESTDIR)$(PREFIX)/bin/clnkr

run: _build-clnkr ## Build and start the CLI
	./clnkr

_build-clnkr:
	go build -trimpath -ldflags '$(LDFLAGS)' -o clnkr ./cmd/clnkr/

##@ Quality
check: _fmt-check _vet _lint _arch sloc frontend-sloc _workflow-make-targets _check-docs test evaluations ## Run formatting, vet, lint, architecture, SLOC, workflow, docs, test, and evaluation checks

test: ## Run all tests
	go test ./... -v

evaluations: build ## Run the mock-provider evaluation suite
	@$(CLANKERVAL_PREFLIGHT) \
	"$$clankerval_path" run --suite default --binary "$(CLANKERVAL_BINARY)"

evaluations-live: build ## Run the live-provider evaluation suite
	@$(CLANKERVAL_PREFLIGHT) \
	CLNKR_EVALUATION_MODE=live-provider "$$clankerval_path" run --suite default --binary "$(CLANKERVAL_BINARY)"

evaluations-live-openai: ## Run the live-provider evaluation suite against OpenAI defaults
	@CLNKR_EVALUATION_API_KEY="$${CLNKR_EVALUATION_OPENAI_API_KEY:-$${OPENAI_API_KEY}}" \
	CLNKR_EVALUATION_BASE_URL="$${CLNKR_EVALUATION_OPENAI_BASE_URL:-https://api.openai.com/v1}" \
	CLNKR_EVALUATION_MODEL="$${CLNKR_EVALUATION_OPENAI_MODEL:-gpt-5.4-nano}" \
	$(MAKE) evaluations-live

evaluations-live-anthropic: ## Run the live-provider evaluation suite against Anthropic defaults
	@CLNKR_EVALUATION_API_KEY="$${CLNKR_EVALUATION_ANTHROPIC_API_KEY:-$${ANTHROPIC_API_KEY}}" \
	CLNKR_EVALUATION_BASE_URL="$${CLNKR_EVALUATION_ANTHROPIC_BASE_URL:-https://api.anthropic.com}" \
	CLNKR_EVALUATION_MODEL="$${CLNKR_EVALUATION_ANTHROPIC_MODEL:-claude-haiku-4-5}" \
	$(MAKE) evaluations-live

_fmt:
	go fmt ./...

_fmt-check:
	@files=$$(find . -type f -name '*.go' -not -path './.git/*'); \
	unformatted=$$(gofmt -l $$files); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: these files are not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

_vet:
	go vet ./...

_lint:
	golangci-lint run ./...

_arch:
	@./scripts/check-architecture-imports.sh

# Repo-root only: counts repo-local Go files in the main-module dependency closure of `.`.
sloc: ## Report core runtime graph SLOC and fail if it exceeds CORE_SLOC_LIMIT
	@sloc="$$(cloc --quiet --csv $$(go list -deps -f '{{if .Module}}{{if .Module.Main}}{{range .GoFiles}}{{$$.Dir}}/{{.}}{{"\n"}}{{end}}{{end}}{{end}}' . | sort -u) | awk -F, 'END { print $$5 }')"; \
	echo "core runtime graph: $$sloc / $(CORE_SLOC_LIMIT) SLOC"; \
	test "$$sloc" -le "$(CORE_SLOC_LIMIT)" || { echo "error: core runtime graph exceeds $(CORE_SLOC_LIMIT) SLOC limit" >&2; exit 1; }

frontend-sloc: ## Report non-test frontend SLOC and fail if it exceeds FRONTEND_SLOC_LIMIT
	@sloc="$$(cloc --quiet --csv --not-match-f='_test\.go$$' cmd | awk -F, 'END { print $$5 }')"; \
	echo "frontend: $$sloc / $(FRONTEND_SLOC_LIMIT) SLOC"; \
	test "$$sloc" -le "$(FRONTEND_SLOC_LIMIT)" || { echo "error: frontend exceeds $(FRONTEND_SLOC_LIMIT) SLOC limit" >&2; exit 1; }

_workflow-make-targets:
	./scripts/check-workflow-make-targets.sh

##@ Contributing
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "Available targets:\n"} \
	/^##@/ {printf "\n%s\n", substr($$0, 5)} \
	/^[a-zA-Z0-9][a-zA-Z0-9_-]*:.*## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

_hooks:
	git config core.hooksPath .githooks

##@ Docs
man: $(DOC_MAN_OUTPUTS) ## Generate man pages from markdown

docs: _site-build ## Build documentation site

docs-serve: _site-sync ## Run documentation site locally
	HUGO_CLNKR_LATEST_TAG='$(LATEST_TAG)' $(HUGO) server --source site

_check-docs: man _site-build

_require-pandoc:
	@command -v "$(PANDOC)" >/dev/null 2>&1 || { \
		echo "error: pandoc is required for docs generation" >&2; \
		exit 1; \
	}

_site-sync:
	find "$(DOC_CONTENT_DIR)" -maxdepth 1 -type f ! -name '_index.md' -delete
	$(MAKE) --no-print-directory $(GENERATED_SITE_DOCS)

_site-build: _site-sync
	HUGO_CLNKR_LATEST_TAG='$(LATEST_TAG)' $(HUGO) --source site

$(DOC_MAN_DIR)/%.1: doc/%.1.md | _require-pandoc
	mkdir -p "$(DOC_MAN_DIR)"
	"$(PANDOC)" --from=markdown-smart --to=man --standalone "$<" -o "$@"

$(DOC_CONTENT_DIR)/clnkr.md: DOC_TITLE = clnkr
$(DOC_CONTENT_DIR)/clnkr.md: DOC_DESCRIPTION = Plain CLI manual page
$(DOC_CONTENT_DIR)/clnkr.md: DOC_WEIGHT = 10

$(DOC_CONTENT_DIR)/clnkr.md: $(DOC_CONTENT_DIR)/%.md: doc/%.1.md $(DOC_PAGE_TEMPLATE) | _require-pandoc
	mkdir -p "$(DOC_CONTENT_DIR)"
	"$(PANDOC)" \
		--from=markdown-smart \
		--to=gfm \
		--standalone \
		--template="$(DOC_PAGE_TEMPLATE)" \
		--metadata title="$(DOC_TITLE)" \
		--metadata description="$(DOC_DESCRIPTION)" \
		--metadata slug="$*" \
		--metadata weight="$(DOC_WEIGHT)" \
		"$<" -o "$@"
