VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN := hew

.DEFAULT_GOAL := build
.PHONY: build install clean test vet fmt lint check run help setup man check-man

build: ## Compile binary
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/hew/

install: ## Install to GOPATH/bin
	go install -ldflags '$(LDFLAGS)' ./cmd/hew/

clean: ## Remove build artifacts
	rm -f $(BIN)

test: ## Run tests
	go test ./... -v

vet: ## Run go vet
	go vet ./...

fmt: ## Format source code
	go fmt ./...

lint: ## Run linters
	golangci-lint run ./...

check: lint test ## Run lint and tests

run: build ## Build and start REPL
	./$(BIN)

man: ## Generate man page from markdown
	go-md2man -in doc/hew.1.md -out doc/hew.1

check-man: ## Verify committed man page is up-to-date
	@cp doc/hew.1 doc/hew.1.bak
	@go-md2man -in doc/hew.1.md -out doc/hew.1
	@diff -q doc/hew.1 doc/hew.1.bak >/dev/null 2>&1 || (echo "error: doc/hew.1 is out of date; run 'make man'" && mv doc/hew.1.bak doc/hew.1 && exit 1)
	@mv doc/hew.1.bak doc/hew.1
	@echo "man page is up-to-date"

help: ## Show available targets
	@grep -E '^[a-z]+:.*##' $(MAKEFILE_LIST) | sort | awk -F ':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

setup: ## Install git hooks
	ln -sf ../../scripts/pre-commit .git/hooks/pre-commit
