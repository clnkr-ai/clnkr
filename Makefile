VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LATEST_TAG ?= $(shell git tag -l '[0-9]*' --sort=-v:refname | head -1)
LDFLAGS := -s -w -X main.version=$(VERSION)
HUGO ?= $(or $(shell command -v hugo 2>/dev/null),$(shell go env GOPATH)/bin/hugo)
PANDOC ?= pandoc
CLANKERVAL_PINNED_VERSION := 0.4.5
CLANKERVAL_BINARY ?= $(CURDIR)/clnkr
CLNKR_ARGS ?=
CLNKR_RUN_CWD ?=
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
	clnkr send readme-image \
	check test evaluations evaluations-live evaluations-live-openai evaluations-live-anthropic \
	help man docs docs-serve \
	_build-clnkr _build-clnkrd \
	_fmt _fmt-check _vet _lint _arch sloc frontend-sloc _workflow-make-targets \
	_hooks _check-docs _require-run-clnkr-tools _require-pandoc _require-readme-image-tools _site-sync _site-build

PREFIX ?= /usr/local
CORE_SLOC_LIMIT := 1705
FRONTEND_SLOC_LIMIT := 1915
DOC_MAN_DIR := build/docs/man
DOC_MAN_OUTPUTS := $(DOC_MAN_DIR)/clnkr.1 $(DOC_MAN_DIR)/clnkrd.1 $(DOC_MAN_DIR)/clnkr.3 $(DOC_MAN_DIR)/clnkr.7
DOC_CONTENT_DIR := site/content/docs
DOC_PAGE_TEMPLATE := site/pandoc/doc-page.md
README_IMAGE := site/static/readme-terminal.png
README_FONT_REPO ?= git@github.com:cosgroveb/berkeley-mono-nerd-font.git
README_FONT_CHECKOUT := build/deps/berkeley-mono-nerd-font
README_FONT := build/readme-fonts/BerkeleyMonoNerdFont-Regular.otf
README_FONT_PATCH_LOG := build/readme-fonts/font-patcher.log
GENERATED_SITE_DOCS := \
	$(DOC_CONTENT_DIR)/clnkr.md \
	$(DOC_CONTENT_DIR)/clnkrd.md

##@ Build
build: _build-clnkr _build-clnkrd ## Build shipped binaries

clean: ## Remove build artifacts
	rm -f clnkr clnkrd
	rm -rf build/docs
	rm -rf build/deps build/readme-fonts
	find "$(DOC_CONTENT_DIR)" -maxdepth 1 -type f ! -name '_index.md' -delete

install: build ## Install shipped binaries
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 clnkr $(DESTDIR)$(PREFIX)/bin/clnkr
	install -m 755 clnkrd $(DESTDIR)$(PREFIX)/bin/clnkrd

run: _build-clnkr ## Build and start the CLI
	./clnkr

clnkr: _build-clnkr _require-run-clnkr-tools ## Build and start the human clnkr wrapper
	CLNKR_RUN_CWD="$(CLNKR_RUN_CWD)" ./scripts/run-clnkr.sh --clnkr-bin "$(CURDIR)/clnkr" -- $(CLNKR_ARGS)

send: _build-clnkr _require-run-clnkr-tools ## Build and start the human clnkr wrapper with --full-send
	CLNKR_RUN_CWD="$(CLNKR_RUN_CWD)" ./scripts/run-clnkr.sh --clnkr-bin "$(CURDIR)/clnkr" -- --full-send $(CLNKR_ARGS)

_build-clnkr:
	go build -trimpath -ldflags '$(LDFLAGS)' -o clnkr ./cmd/clnkr/

_build-clnkrd:
	go build -trimpath -ldflags '$(LDFLAGS)' -o clnkrd ./cmd/clnkrd/

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

readme-image: _require-readme-image-tools ## Render README terminal image
	@mkdir -p "$(dir $(README_FONT))" "$(dir $(README_FONT_CHECKOUT))"
	@if [ -d "$(README_FONT_CHECKOUT)/.git" ]; then \
		git -C "$(README_FONT_CHECKOUT)" fetch --quiet origin main; \
		git -C "$(README_FONT_CHECKOUT)" checkout --quiet main; \
		git -C "$(README_FONT_CHECKOUT)" reset --quiet --hard origin/main; \
	else \
		git clone --quiet "$(README_FONT_REPO)" "$(README_FONT_CHECKOUT)"; \
	fi
	rm -f "$(README_FONT)" "$(README_FONT_PATCH_LOG)"
	@fontforge -script "$(README_FONT_CHECKOUT)/font-patcher" --complete --has-no-italic \
		-out "$(dir $(README_FONT))" "$(README_FONT_CHECKOUT)/BerkeleyMono-Regular.otf" \
		>"$(README_FONT_PATCH_LOG)" 2>&1 || { \
			echo "error: failed to patch README font; see $(README_FONT_PATCH_LOG)" >&2; \
			tail -40 "$(README_FONT_PATCH_LOG)" >&2; \
			exit 1; \
		}
	CLNKR_README_IMAGE_FONT="$(CURDIR)/$(README_FONT)" ./scripts/render-readme-banner-png.sh "$(README_IMAGE)"

_check-docs: man _site-build

_require-pandoc:
	@command -v "$(PANDOC)" >/dev/null 2>&1 || { \
		echo "error: pandoc is required for docs generation" >&2; \
		exit 1; \
	}

_require-run-clnkr-tools:
	@command -v gum >/dev/null 2>&1 || { \
		echo "error: gum is required for clnkr wrapper" >&2; \
		exit 1; \
	}
	@command -v jq >/dev/null 2>&1 || { \
		echo "error: jq is required for clnkr wrapper" >&2; \
		exit 1; \
	}

_require-readme-image-tools:
	@command -v git >/dev/null 2>&1 || { \
		echo "error: git is required for README image font checkout" >&2; \
		exit 1; \
	}
	@command -v gum >/dev/null 2>&1 || { \
		echo "error: gum is required for README image generation" >&2; \
		echo "install it from https://github.com/charmbracelet/gum" >&2; \
		exit 1; \
	}
	@command -v fontforge >/dev/null 2>&1 || { \
		echo "error: fontforge is required for README image font patching" >&2; \
		echo "Ubuntu: sudo apt-get install fontforge" >&2; \
		echo "macOS: brew install fontforge" >&2; \
		exit 1; \
	}
	@{ command -v magick >/dev/null 2>&1 || command -v convert >/dev/null 2>&1; } || { \
		echo "error: ImageMagick is required for README image generation" >&2; \
		echo "Ubuntu: sudo apt-get install imagemagick" >&2; \
		echo "macOS: brew install imagemagick" >&2; \
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

$(DOC_MAN_DIR)/%.3: doc/%.3.md | _require-pandoc
	mkdir -p "$(DOC_MAN_DIR)"
	"$(PANDOC)" --from=markdown-smart --to=man --standalone "$<" -o "$@"

$(DOC_MAN_DIR)/%.7: doc/%.7.md | _require-pandoc
	mkdir -p "$(DOC_MAN_DIR)"
	"$(PANDOC)" --from=markdown-smart --to=man --standalone "$<" -o "$@"

$(DOC_CONTENT_DIR)/clnkr.md: DOC_TITLE = clnkr
$(DOC_CONTENT_DIR)/clnkr.md: DOC_DESCRIPTION = Plain CLI manual page
$(DOC_CONTENT_DIR)/clnkr.md: DOC_WEIGHT = 10
$(DOC_CONTENT_DIR)/clnkrd.md: DOC_TITLE = clnkrd
$(DOC_CONTENT_DIR)/clnkrd.md: DOC_DESCRIPTION = Stdio JSONL adapter manual page
$(DOC_CONTENT_DIR)/clnkrd.md: DOC_WEIGHT = 11

$(DOC_CONTENT_DIR)/clnkr.md $(DOC_CONTENT_DIR)/clnkrd.md: $(DOC_CONTENT_DIR)/%.md: doc/%.1.md $(DOC_PAGE_TEMPLATE) | _require-pandoc
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
