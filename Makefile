VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LATEST_TAG ?= $(shell git tag -l 'v[0-9]*' --sort=-v:refname | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
HUGO ?= $(or $(shell command -v hugo 2>/dev/null),$(shell go env GOPATH)/bin/hugo)
PANDOC ?= pandoc
CLANKERVAL_PINNED_VERSION := 0.4.3
CLANKERVAL_DOCS_REPO_URL ?= https://github.com/clnkr-ai/clankerval.git

.DEFAULT_GOAL := build
.PHONY: \
	build clean install run \
	check test evaluations evaluations-live evaluations-live-openai evaluations-live-anthropic \
	help man docs docs-serve \
	_build-clnku _build-clnkr \
	_fmt _fmt-check _vet _lint _arch sloc _workflow-make-targets \
	_hooks _check-docs _require-pandoc _site-sync _site-build

PREFIX ?= /usr/local
CORE_SLOC_LIMIT := 1300
DEFERRED_PACKAGE_ALLOWLIST := scripts/deferred-package-allowlist.txt
DOC_MAN_DIR := build/docs/man
DOC_MAN_OUTPUTS := $(DOC_MAN_DIR)/clnkr.1 $(DOC_MAN_DIR)/clnku.1
DOC_CONTENT_DIR := site/content/docs
GENERATED_SITE_DOCS := \
	$(DOC_CONTENT_DIR)/clnkr.md \
	$(DOC_CONTENT_DIR)/clnku.md \
	$(DOC_CONTENT_DIR)/clankerval.md

##@ Build
build: _build-clnku _build-clnkr ## Build shipped binaries

clean: ## Remove build artifacts
	rm -f clnku clnkr
	rm -rf build/docs
	rm -f $(GENERATED_SITE_DOCS)

install: build ## Install shipped binaries and clnk symlink
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 clnkr $(DESTDIR)$(PREFIX)/bin/clnkr
	install -m 755 clnku $(DESTDIR)$(PREFIX)/bin/clnku
	ln -sf clnkr $(DESTDIR)$(PREFIX)/bin/clnk

run: _build-clnkr ## Build and start TUI
	./clnkr

_build-clnku:
	go build -trimpath -ldflags '$(LDFLAGS)' -o clnku ./cmd/clnku/

_build-clnkr:
	cd cmd/clnkr && go build -trimpath -ldflags '$(LDFLAGS)' -o ../../clnkr .

##@ Quality
check: _fmt-check _vet _lint _arch sloc _workflow-make-targets _check-docs test evaluations ## Run formatting, vet, lint, architecture, SLOC, workflow, docs, test, and evaluation checks

test: ## Run all tests
	go test ./... -v
	cd cmd/clnkr && go test ./... -v

evaluations: ## Run the mock-provider evaluation suite
	@version_output="$$(clankerval --version 2>&1 || true)"; \
	[ "$$version_output" = "clankerval $(CLANKERVAL_PINNED_VERSION)" ] || { \
		echo "error: expected 'clankerval $(CLANKERVAL_PINNED_VERSION)' from 'clankerval --version', got '$$version_output'" >&2; \
		echo "download it from https://github.com/clnkr-ai/clankerval/releases" >&2; \
		exit 1; \
	}; \
	clankerval run --suite default

evaluations-live: ## Run the live-provider evaluation suite
	@version_output="$$(clankerval --version 2>&1 || true)"; \
	[ "$$version_output" = "clankerval $(CLANKERVAL_PINNED_VERSION)" ] || { \
		echo "error: expected 'clankerval $(CLANKERVAL_PINNED_VERSION)' from 'clankerval --version', got '$$version_output'" >&2; \
		echo "download it from https://github.com/clnkr-ai/clankerval/releases" >&2; \
		exit 1; \
	}; \
	CLNKR_EVALUATION_MODE=live-provider clankerval run --suite default

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
	cd cmd/clnkr && go fmt ./...

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
	cd cmd/clnkr && go vet ./...

_lint:
	golangci-lint run ./...
	cd cmd/clnkr && golangci-lint run ./...

_arch:
	@./scripts/check-architecture-imports.py $(DEFERRED_PACKAGE_ALLOWLIST)

# Repo-root only: counts repo-local Go files in the main-module dependency closure of `.`.
sloc: ## Report core runtime graph SLOC and fail if it exceeds CORE_SLOC_LIMIT
	@sloc="$$(cloc --quiet --csv $$(go list -deps -f '{{if .Module}}{{if .Module.Main}}{{range .GoFiles}}{{$$.Dir}}/{{.}}{{"\n"}}{{end}}{{end}}{{end}}' . | sort -u) | awk -F, 'END { print $$5 }')"; \
	echo "core runtime graph: $$sloc / $(CORE_SLOC_LIMIT) SLOC"; \
	test "$$sloc" -le "$(CORE_SLOC_LIMIT)" || { echo "error: core runtime graph exceeds $(CORE_SLOC_LIMIT) SLOC limit" >&2; exit 1; }

_workflow-make-targets:
	python3 ./scripts/check-workflow-make-targets.py

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
	rm -f $(GENERATED_SITE_DOCS)
	$(MAKE) --no-print-directory $(GENERATED_SITE_DOCS)

_site-build: _site-sync
	HUGO_CLNKR_LATEST_TAG='$(LATEST_TAG)' $(HUGO) --source site

$(DOC_MAN_DIR)/%.1: doc/%.1.md | _require-pandoc
	mkdir -p "$(DOC_MAN_DIR)"
	"$(PANDOC)" --from=markdown-smart --to=man --standalone "$<" -o "$@"

$(DOC_CONTENT_DIR)/clnkr.md: DOC_TITLE = clnkr
$(DOC_CONTENT_DIR)/clnkr.md: DOC_DESCRIPTION = Terminal UI manual page
$(DOC_CONTENT_DIR)/clnkr.md: DOC_WEIGHT = 10
$(DOC_CONTENT_DIR)/clnku.md: DOC_TITLE = clnku
$(DOC_CONTENT_DIR)/clnku.md: DOC_DESCRIPTION = Plain CLI manual page
$(DOC_CONTENT_DIR)/clnku.md: DOC_WEIGHT = 10

$(DOC_CONTENT_DIR)/clnkr.md $(DOC_CONTENT_DIR)/clnku.md: $(DOC_CONTENT_DIR)/%.md: doc/%.1.md | _require-pandoc
	mkdir -p "$(DOC_CONTENT_DIR)"
	{ \
		printf '+++\n'; \
		printf 'title = "%s"\n' "$(DOC_TITLE)"; \
		printf 'description = "%s"\n' "$(DOC_DESCRIPTION)"; \
		printf 'slug = "%s"\n' "$*"; \
		printf 'weight = %s\n' "$(DOC_WEIGHT)"; \
		printf '+++\n\n'; \
		"$(PANDOC)" --from=markdown-smart --to=gfm "$<"; \
	} > "$@"

$(DOC_CONTENT_DIR)/clankerval.md: | _require-pandoc
	tmpdir="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	git clone --depth 1 "$(CLANKERVAL_DOCS_REPO_URL)" "$$tmpdir"; \
	src="$$tmpdir/doc/clankerval.1.md"; \
	[ -f "$$src" ] || { echo "error: missing clankerval manpage at $$src" >&2; exit 1; }; \
	normalized="$$tmpdir/clankerval.pandoc.md"; \
	{ \
		printf '%% clankerval(1) User Commands\n\n'; \
		tail -n +4 "$$src"; \
	} > "$$normalized"; \
	mkdir -p "$(DOC_CONTENT_DIR)"; \
	{ \
		printf '+++\n'; \
		printf 'title = "clankerval"\n'; \
		printf 'description = "Evaluation runner manual page"\n'; \
		printf 'slug = "clankerval"\n'; \
		printf 'weight = 20\n'; \
		printf '+++\n\n'; \
		"$(PANDOC)" --from=markdown-smart --to=gfm "$$normalized"; \
	} > "$@"
