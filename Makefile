VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LATEST_TAG ?= $(shell git tag -l 'v[0-9]*' --sort=-v:refname | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
HUGO ?= $(or $(shell command -v hugo 2>/dev/null),$(shell go env GOPATH)/bin/hugo)

.DEFAULT_GOAL := build
.PHONY: \
	build clean install run \
	check test evaluations evaluations-live \
	help man docs docs-serve \
	_build-clnku _build-clnkr \
	_fmt _fmt-check _vet _lint _sloc _workflow-make-targets \
	_hooks _check-man _site-sync _site-build

PREFIX ?= /usr/local
CORE_SLOC_LIMIT := 750
CORE_FILES := $(filter-out %_test.go,$(wildcard *.go))

##@ Build
build: _build-clnku _build-clnkr ## Build both binaries

clean: ## Remove build artifacts
	rm -f clnku clnkr

install: build ## Install both binaries and clnk symlink
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
check: _fmt-check _vet _lint _sloc _workflow-make-targets _check-man test ## Run formatting, vet, lint, SLOC, workflow, manpage, and test checks

test: ## Run all tests
	go test ./... -v
	cd cmd/clnkr && go test ./... -v

evaluations: ## Run the mock-provider evaluation suite
	go run ./cmd/clnkeval run --suite default

evaluations-live: ## Run the live-provider evaluation suite
	CLNKR_EVALUATION_MODE=live-provider go run ./cmd/clnkeval run --suite default

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

_sloc:
	@sloc=$$(cloc --quiet --csv $(CORE_FILES) | tail -1 | cut -d, -f5); \
	echo "core library: $$sloc / $(CORE_SLOC_LIMIT) SLOC"; \
	if [ "$$sloc" -gt $(CORE_SLOC_LIMIT) ]; then \
		echo "error: core exceeds $(CORE_SLOC_LIMIT) SLOC limit" >&2; exit 1; \
	fi

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
