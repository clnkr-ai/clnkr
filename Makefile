VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LATEST_TAG ?= $(shell git tag -l 'v[0-9]*' --sort=-v:refname | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
HUGO ?= $(or $(shell command -v hugo 2>/dev/null),$(shell go env GOPATH)/bin/hugo)
CLANKERVAL_MIN_VERSION := 0.4.0
CLANKERVAL_PREFLIGHT = \
	compare_versions() { \
		have_raw="$$1"; \
		need_raw="$$2"; \
		have_core="$${have_raw%%[-+]*}"; \
		need_core="$${need_raw%%[-+]*}"; \
		old_ifs="$$IFS"; \
		IFS=.; \
		set -- $$have_core; \
		IFS="$$old_ifs"; \
		have_major="$${1:-0}"; \
		have_minor="$${2:-0}"; \
		have_patch="$${3:-0}"; \
		IFS=.; \
		set -- $$need_core; \
		IFS="$$old_ifs"; \
		need_major="$${1:-0}"; \
		need_minor="$${2:-0}"; \
		need_patch="$${3:-0}"; \
		if [ "$$have_major" -lt "$$need_major" ]; then return 1; fi; \
		if [ "$$have_major" -gt "$$need_major" ]; then return 0; fi; \
		if [ "$$have_minor" -lt "$$need_minor" ]; then return 1; fi; \
		if [ "$$have_minor" -gt "$$need_minor" ]; then return 0; fi; \
		if [ "$$have_patch" -lt "$$need_patch" ]; then return 1; fi; \
		if [ "$$have_patch" -gt "$$need_patch" ]; then return 0; fi; \
		case "$$have_raw" in \
			*-*) return 1 ;; \
		esac; \
		return 0; \
	}; \
	clankerval_path="$$(command -v clankerval 2>/dev/null || true)"; \
	if [ -z "$$clankerval_path" ]; then \
		echo "error: clankerval >= $(CLANKERVAL_MIN_VERSION) is required." >&2; \
		exit 1; \
	fi; \
	if ! version_output="$$("$$clankerval_path" --version 2>&1)"; then \
		echo "error: clankerval --version failed: $$version_output" >&2; \
		exit 1; \
	fi; \
	version="$$(printf '%s\n' "$$version_output" | awk 'match($$0, /v?[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?/) { version = substr($$0, RSTART, RLENGTH) } END { sub(/^v/, "", version); print version }')"; \
	if [ -z "$$version" ]; then \
		echo "error: failed to parse clankerval version from: $$version_output" >&2; \
		exit 1; \
	fi; \
	if ! compare_versions "$$version" "$(CLANKERVAL_MIN_VERSION)"; then \
		echo "error: clankerval >= $(CLANKERVAL_MIN_VERSION) is required." >&2; \
		echo "found clankerval $$version at $$clankerval_path" >&2; \
		exit 1; \
	fi;

.DEFAULT_GOAL := build
.PHONY: \
	build clean install run \
	check test evaluations evaluations-live evaluations-live-openai evaluations-live-anthropic \
	help man docs docs-serve \
	_build-clnku _build-clnkr \
	_fmt _fmt-check _vet _lint _arch _sloc _workflow-make-targets \
	_hooks _check-man _site-sync _site-build

PREFIX ?= /usr/local
CORE_SLOC_LIMIT := 1300
CORE_RUNTIME_MANIFEST := scripts/core-runtime-packages.txt
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
check: _fmt-check _vet _lint _arch _sloc _workflow-make-targets _check-man test evaluations ## Run formatting, vet, lint, architecture, SLOC, workflow, manpage, test, and evaluation checks

test: ## Run all tests
	./scripts/test_make_evaluations.sh
	go test ./... -v
	cd cmd/clnkr && go test ./... -v

evaluations: ## Run the mock-provider evaluation suite
	@$(CLANKERVAL_PREFLIGHT) \
	"$$clankerval_path" run --suite default

evaluations-live: ## Run the live-provider evaluation suite
	@$(CLANKERVAL_PREFLIGHT) \
	CLNKR_EVALUATION_MODE=live-provider "$$clankerval_path" run --suite default

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

_sloc:
	@CORE_SLOC_LIMIT=$(CORE_SLOC_LIMIT) ./scripts/check-core-sloc.sh $(CORE_RUNTIME_MANIFEST)

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
