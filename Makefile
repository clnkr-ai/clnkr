VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
HUGO ?= $(or $(shell command -v hugo 2>/dev/null),$(shell go env GOPATH)/bin/hugo)

.DEFAULT_GOAL := build-all
.PHONY: build-clnku build-clnkr build-all install clean test vet fmt lint sloc check run help setup man check-man site-sync site-build site-serve

build-clnku: ## Build plain CLI binary (clnku)
	go build -trimpath -ldflags '$(LDFLAGS)' -o clnku ./cmd/clnku/

build-clnkr: ## Build TUI binary (clnkr)
	cd cmd/clnkr && go build -trimpath -ldflags '$(LDFLAGS)' -o ../../clnkr .

build-all: build-clnku build-clnkr ## Build both binaries

PREFIX ?= /usr/local

install: build-all ## Install both binaries and clnk symlink
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 clnkr $(DESTDIR)$(PREFIX)/bin/clnkr
	install -m 755 clnku $(DESTDIR)$(PREFIX)/bin/clnku
	ln -sf clnkr $(DESTDIR)$(PREFIX)/bin/clnk

clean: ## Remove build artifacts
	rm -f clnku clnkr

test: ## Run all tests
	go test ./... -v
	cd cmd/clnkr && go test ./... -v

vet: ## Run go vet
	go vet ./...
	cd cmd/clnkr && go vet ./...

fmt: ## Format source code
	go fmt ./...
	cd cmd/clnkr && go fmt ./...

lint: sloc ## Run linters
	golangci-lint run ./...
	cd cmd/clnkr && golangci-lint run ./...

CORE_SLOC_LIMIT := 500
CORE_FILES := $(filter-out %_test.go,$(wildcard *.go))

sloc: ## Check core library stays under $(CORE_SLOC_LIMIT) SLOC
	@sloc=$$(cloc --quiet --csv $(CORE_FILES) | tail -1 | cut -d, -f5); \
	echo "core library: $$sloc / $(CORE_SLOC_LIMIT) SLOC"; \
	if [ "$$sloc" -gt $(CORE_SLOC_LIMIT) ]; then \
		echo "error: core exceeds $(CORE_SLOC_LIMIT) SLOC limit" >&2; exit 1; \
	fi

check: lint sloc test ## Run lint, SLOC check, and tests

run: build-clnkr ## Build and start TUI
	./clnkr

man: ## Generate man pages from markdown
	go-md2man -in doc/clnkr.1.md -out doc/clnkr.1
	go-md2man -in doc/clnku.1.md -out doc/clnku.1

check-man: ## Verify committed man pages are up-to-date
	@cp doc/clnkr.1 doc/clnkr.1.bak
	@go-md2man -in doc/clnkr.1.md -out doc/clnkr.1
	@diff -q doc/clnkr.1 doc/clnkr.1.bak >/dev/null 2>&1 || (echo "error: doc/clnkr.1 is out of date; run 'make man'" && mv doc/clnkr.1.bak doc/clnkr.1 && exit 1)
	@mv doc/clnkr.1.bak doc/clnkr.1
	@cp doc/clnku.1 doc/clnku.1.bak
	@go-md2man -in doc/clnku.1.md -out doc/clnku.1
	@diff -q doc/clnku.1 doc/clnku.1.bak >/dev/null 2>&1 || (echo "error: doc/clnku.1 is out of date; run 'make man'" && mv doc/clnku.1.bak doc/clnku.1 && exit 1)
	@mv doc/clnku.1.bak doc/clnku.1
	@echo "man pages are up-to-date"

site-sync: ## Sync doc/*.md into Hugo content
	./scripts/sync-site-docs.sh

site-build: site-sync ## Build the Hugo site into site/public
	$(HUGO) --source site

site-serve: site-sync ## Run the Hugo development server
	$(HUGO) server --source site

help: ## Show available targets
	@grep -E '^[a-z][-a-z]+:.*##' $(MAKEFILE_LIST) | sort | awk -F ':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

setup: ## Install git hooks
	ln -sf ../../scripts/pre-commit .git/hooks/pre-commit
