VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := build-all
.PHONY: build build-hui build-all install clean test vet fmt lint check run help setup man check-man

build: ## Build plain CLI binary
	go build -trimpath -ldflags '$(LDFLAGS)' -o hew ./cmd/hew/

build-hui: ## Build TUI binary
	cd cmd/hui && go build -trimpath -ldflags '$(LDFLAGS)' -o ../../hui .

build-all: build build-hui ## Build both binaries

install: build-all ## Install both binaries
	go install -ldflags '$(LDFLAGS)' ./cmd/hew/
	cd cmd/hui && go install -ldflags '$(LDFLAGS)' .

clean: ## Remove build artifacts
	rm -f hew hui

test: ## Run all tests
	go test ./... -v
	cd cmd/hui && go test ./... -v

vet: ## Run go vet
	go vet ./...
	cd cmd/hui && go vet ./...

fmt: ## Format source code
	go fmt ./...
	cd cmd/hui && go fmt ./...

lint: ## Run linters
	golangci-lint run ./...
	cd cmd/hui && golangci-lint run ./...

check: lint test ## Run lint and tests

run: build-hui ## Build and start TUI
	./hui

man: ## Generate man page from markdown
	go-md2man -in doc/hew.1.md -out doc/hew.1

check-man: ## Verify committed man page is up-to-date
	@cp doc/hew.1 doc/hew.1.bak
	@go-md2man -in doc/hew.1.md -out doc/hew.1
	@diff -q doc/hew.1 doc/hew.1.bak >/dev/null 2>&1 || (echo "error: doc/hew.1 is out of date; run 'make man'" && mv doc/hew.1.bak doc/hew.1 && exit 1)
	@mv doc/hew.1.bak doc/hew.1
	@echo "man page is up-to-date"

help: ## Show available targets
	@grep -E '^[a-z][-a-z]+:.*##' $(MAKEFILE_LIST) | sort | awk -F ':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

setup: ## Install git hooks
	ln -sf ../../scripts/pre-commit .git/hooks/pre-commit
