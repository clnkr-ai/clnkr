VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN := hew

.DEFAULT_GOAL := build
.PHONY: build install clean test vet fmt check run help

build: ## Compile binary
	go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/hew/

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

check: vet test ## Run vet and tests

run: build ## Build and start REPL
	./$(BIN)

help: ## Show available targets
	@grep -E '^[a-z]+:.*##' $(MAKEFILE_LIST) | sort | awk -F ':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'
