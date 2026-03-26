VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := build-all
.PHONY: build-hu build-hew build-all install clean test vet fmt lint sloc check run help setup man check-man

build-hu: ## Build plain CLI binary (hu)
	go build -trimpath -ldflags '$(LDFLAGS)' -o hu ./cmd/hu/

build-hew: ## Build TUI binary (hew)
	cd cmd/hew && go build -trimpath -ldflags '$(LDFLAGS)' -o ../../hew .

build-all: build-hu build-hew ## Build both binaries

install: build-all ## Install both binaries
	go install -ldflags '$(LDFLAGS)' ./cmd/hu/
	cd cmd/hew && go install -ldflags '$(LDFLAGS)' .

clean: ## Remove build artifacts
	rm -f hu hew

test: ## Run all tests
	go test ./... -v
	cd cmd/hew && go test ./... -v

vet: ## Run go vet
	go vet ./...
	cd cmd/hew && go vet ./...

fmt: ## Format source code
	go fmt ./...
	cd cmd/hew && go fmt ./...

lint: ## Run linters
	golangci-lint run ./...
	cd cmd/hew && golangci-lint run ./...

CORE_SLOC_LIMIT := 500
CORE_FILES := $(filter-out %_test.go,$(wildcard *.go))

sloc: ## Check core library stays under $(CORE_SLOC_LIMIT) SLOC
	@sloc=$$(cloc --quiet --csv $(CORE_FILES) | tail -1 | cut -d, -f5); \
	echo "core library: $$sloc / $(CORE_SLOC_LIMIT) SLOC"; \
	if [ "$$sloc" -gt $(CORE_SLOC_LIMIT) ]; then \
		echo "error: core exceeds $(CORE_SLOC_LIMIT) SLOC limit" >&2; exit 1; \
	fi

check: lint sloc test ## Run lint, SLOC check, and tests

run: build-hew ## Build and start TUI
	./hew

man: ## Generate man pages from markdown
	go-md2man -in doc/hew.1.md -out doc/hew.1
	go-md2man -in doc/hu.1.md -out doc/hu.1

check-man: ## Verify committed man pages are up-to-date
	@cp doc/hew.1 doc/hew.1.bak
	@go-md2man -in doc/hew.1.md -out doc/hew.1
	@diff -q doc/hew.1 doc/hew.1.bak >/dev/null 2>&1 || (echo "error: doc/hew.1 is out of date; run 'make man'" && mv doc/hew.1.bak doc/hew.1 && exit 1)
	@mv doc/hew.1.bak doc/hew.1
	@cp doc/hu.1 doc/hu.1.bak
	@go-md2man -in doc/hu.1.md -out doc/hu.1
	@diff -q doc/hu.1 doc/hu.1.bak >/dev/null 2>&1 || (echo "error: doc/hu.1 is out of date; run 'make man'" && mv doc/hu.1.bak doc/hu.1 && exit 1)
	@mv doc/hu.1.bak doc/hu.1
	@echo "man pages are up-to-date"

help: ## Show available targets
	@grep -E '^[a-z][-a-z]+:.*##' $(MAKEFILE_LIST) | sort | awk -F ':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

setup: ## Install git hooks
	ln -sf ../../scripts/pre-commit .git/hooks/pre-commit
