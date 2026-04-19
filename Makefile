VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LATEST_TAG ?= $(shell git tag -l 'v[0-9]*' --sort=-v:refname | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
HUGO ?= $(or $(shell command -v hugo 2>/dev/null),$(shell go env GOPATH)/bin/hugo)
CLANKERVAL_PINNED_VERSION := 0.4.3

.DEFAULT_GOAL := build
.PHONY: \
	build clean install run \
	check test evaluations evaluations-live evaluations-live-openai evaluations-live-anthropic \
	help man docs docs-serve \
	_build-clnku _build-clnkr \
	_fmt _fmt-check _vet _lint _arch sloc _workflow-make-targets \
	_hooks _check-man _site-sync _site-build

PREFIX ?= /usr/local
CORE_SLOC_LIMIT := 1300
DEFERRED_PACKAGE_ALLOWLIST := scripts/deferred-package-allowlist.txt

##@ Build
build: _build-clnku _build-clnkr ## Build shipped binaries

clean: ## Remove build artifacts
	rm -f clnku clnkr

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
check: _fmt-check _vet _lint _arch sloc _workflow-make-targets _check-man test evaluations ## Run formatting, vet, lint, architecture, SLOC, workflow, manpage, test, and evaluation checks

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
man: ## Generate man pages from markdown
	go-md2man -in doc/clnkr.1.md -out doc/clnkr.1
	go-md2man -in doc/clnku.1.md -out doc/clnku.1

docs: _site-build ## Build documentation site

docs-serve: _site-sync ## Run documentation site locally
	HUGO_CLNKR_LATEST_TAG='$(LATEST_TAG)' $(HUGO) server --source site

_check-man:
	@cp doc/clnkr.1 doc/clnkr.1.bak
	@go-md2man -in doc/clnkr.1.md -out doc/clnkr.1
	@diff -q doc/clnkr.1 doc/clnkr.1.bak >/dev/null 2>&1 || (echo "error: doc/clnkr.1 is out of date; run 'make man'" && mv doc/clnkr.1.bak doc/clnkr.1 && exit 1)
	@mv doc/clnkr.1.bak doc/clnkr.1
	@cp doc/clnku.1 doc/clnku.1.bak
	@go-md2man -in doc/clnku.1.md -out doc/clnku.1
	@diff -q doc/clnku.1 doc/clnku.1.bak >/dev/null 2>&1 || (echo "error: doc/clnku.1 is out of date; run 'make man'" && mv doc/clnku.1.bak doc/clnku.1 && exit 1)
	@mv doc/clnku.1.bak doc/clnku.1
	@echo "man pages are up-to-date"

_site-sync:
	./scripts/sync-site-docs.sh

_site-build: _site-sync
	HUGO_CLNKR_LATEST_TAG='$(LATEST_TAG)' $(HUGO) --source site
