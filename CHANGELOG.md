# Changelog

## 1.4.5 (2026-04-04)

- Add interactive progress output to clnkeval

## 1.4.4 (2026-04-04)

- Improve clnkeval CLI UX: add --help, --version, and usage text
- Update CHANGELOG.md for 1.4.3

## 1.4.3 (2026-04-04)

- Add outcome_command_output grader for language-agnostic workspace verification
- Support running evals against external repos with pre-installed binary

## 1.4.2 (2026-04-04)


## 1.4.1 (2026-04-04)

- Use fmt.Fprintf in ExportJUnit
- Ship clnkeval as a first-class binary alongside clnkr and clnku
- Add evaluations runtime and mock-provider mode
- Update CHANGELOG.md for 1.4.0
- Update CHANGELOG.md for 1.4.0

## 1.4.0 (2026-04-01)

- Retry OpenAI rate limit responses
- Update CHANGELOG.md for 1.3.2

## 1.3.2 (2026-04-01)

- Refresh man pages and README for current features
- Update CHANGELOG.md for 1.3.1

## 1.3.1 (2026-04-01)

- Bound compaction summarize prompt size
- gitignore update
- Document local clnkr skills workflow
- Remove CommandExecutor timeouts
- Update CHANGELOG.md for 1.3.0

## 1.3.0 (2026-04-01)

- Add /delegate workflow to clnkr TUI
- Update CHANGELOG.md for 1.2.2

## 1.2.2 (2026-04-01)

- End compaction summarize requests with a user message
- Add evaluations/ to .gitignore
- Update CHANGELOG.md for 1.2.1

## 1.2.1 (2026-04-01)

- Request terminal focus reports in the TUI
- Link the site footer
- Tighten homepage install copy
- Update CHANGELOG.md for 1.2.0
- Use package manager links on the homepage

## 1.2.0 (2026-03-31)

- Add manual transcript compaction
- Update CHANGELOG.md for 1.1.0

## 1.1.0 (2026-03-31)

- Render protocol turns in TUI instead of raw JSON
- Ignore local worktrees directory
- Update CHANGELOG.md for 1.0.3
- Render resumed history in TUI
- Update CHANGELOG.md for 1.0.2

## 1.0.3 (2026-03-31)

- Render resumed history in TUI

## 1.0.2 (2026-03-31)

- Match TUI styling to retro site
- Redesign site with retro terminal shell
- Update GitHub Actions versions
- Fix actionlint workflow warnings
- Lint workflow make targets
- Fix CI make targets
- Clean up make help targets
- Document Unix-only support
- Fix TUI shutdown race and add PTY tests
- Relax live eval command matching
- Upload live eval artifacts on failure
- Clean up old --full-send implementation docs
- Add eval harness with live mode
- Update CHANGELOG.md for 1.0.1

## 1.0.1 (2026-03-29)

- Persist cwd in transcript state and refine approval UX
- Remove worktree guidance for local clnkr dev
- Update CHANGELOG.md for 1.0.0

## 1.0.0 (2026-03-29)

- Add per-command approval mode
- Update CHANGELOG.md for 0.9.0

## 0.9.0 (2026-03-29)

- Update CHANGELOG.md for 0.9.0
- Harden bash turn parsing and shell state (#2)
- Add Hugo site and gh-pages deployment
- Update CHANGELOG.md for 0.8.0

## 0.8.0 (2026-03-26)

- Add clnk symlink to install target
- Update CI and release workflows for clnkr
- Update documentation for clnkr rename
- Rename directories, env vars, paths, and strings to clnkr/clnku
- Rename Go module and package from hew to clnkr
- Update CHANGELOG.md for 0.7.0
- Ignore .claude directory except settings.json
- Replace bash fence parser with structured JSON turn protocol
- Run SLOC check as part of make lint
- Cap core library at 500 SLOC
- Update CHANGELOG.md for 0.6.1
- Clarify before exploring without a task
- Ignore mixed done signal until next turn
- Fix clarification guard after squash merge
- Make command output clearly not human input
- Handle clarification as a distinct paused state
- Update CHANGELOG.md for 0.6.0
- Remove hardcoded workflow prompts, add system prompt control flags
- Update CHANGELOG.md for 0.5.6
- Add grep flag guidance to RLM child instruction rules
- Update CHANGELOG.md for 0.5.5
- Prescriptive child instructions in RLM prompt
- Update CHANGELOG.md for 0.5.4
- Strengthen RLM prompt: require hu -p for subtasks, ban inline for-loops
- Update CHANGELOG.md for 0.5.3
- Execute all bash blocks in a single LLM response
- Update CHANGELOG.md for 0.5.2
- Update CHANGELOG.md for 0.5.2
- Rewrite --rlm prompt based on eval results
- Update CHANGELOG.md for 0.5.1
- Update CHANGELOG.md for 0.5.1
- Add --rlm flag for recursive decomposition workflow
- Update CHANGELOG.md for 0.5.0
- Add --disable-planning-workflow flag to hu, update both man pages
- Update CHANGELOG.md for 0.5.0
- Add hu(1) man page, update hew(1) for TUI
- Rename binaries: hew (plain CLI) -> hu, hui (TUI) -> hew
- Update CHANGELOG.md for 0.4.2
- Load AGENTS.md from three layered locations
- fix: suppress errcheck on os.Unsetenv in session test
- Revert "Add multi-layer binary symmetry enforcement system"
- Update CHANGELOG.md for 0.4.1
- Add brainstorming and writing-plans sections to planning workflow prompt
- Update CHANGELOG.md for 0.4.0
- Promote planning workflow to system prompt with hui-only opt-out
- Add multi-layer binary symmetry enforcement system
- fix: add missing session auto-save to hui conversational mode
- Update CHANGELOG.md for 0.3.1
- Update CHANGELOG.md for 0.3.0
- Add session persistence as composable session/ package
- Add Agent Orchestration Patterns section to AGENTS.md
- Update CHANGELOG.md for 0.2.2
- Fix: prevent empty content blocks in message history
- Update CHANGELOG.md for 0.2.1
- Overhaul system prompt and replace exit signal with <done/> tag
- Double HTTP client timeout to 240 seconds
- Isildur claimed the Tool as his own
- Update CHANGELOG.md for 0.2.0
- Build and release hui TUI binary alongside hew (#2)
- Add hui TUI and rename hew-core back to hew
- Update CHANGELOG.md for 0.1.3
- Add rule: CHANGELOG.md is generated by release pipeline
- Extract human-readable message from API error responses
- Rename CLAUDE.md to AGENTS.md, symlink CLAUDE.md for compatibility
- Update CHANGELOG.md for 0.1.2
- Update CHANGELOG.md for 0.1.2
- Stop hardcoding /v1 in OpenAI adapter URL path
- Update CHANGELOG.md for 0.1.1
- Add SECURITY.md, artifact attestation, changelog generation, and Homebrew tap
- Address review feedback and update docs for orchestration flags
- Add --load-messages flag to seed conversation from file
- Add --trajectory flag to dump message history on exit
- Add --event-log flag to write JSONL events to file
- Add ProcessGroup field to CommandExecutor for child isolation
- Add AddMessages to seed agent conversation history
- Add worktree directory preference to CLAUDE.md
- Add HEW_BASE_URL and HEW_MODEL env vars
- Restore gbp dch and arm64 deb, fix runner label
- Make release job idempotent for reruns
- Add devscripts to CI apt install for dch
- Drop arm64 deb job, arm64 runners unavailable on free plan
- Fix artifact upload: copy debs into workspace before upload
- Fix release CI: use dch instead of gbp dch, real maintainer identity
- Fix gbp dch: specify --debian-branch=debian/main
- Add release workflow for Debian packages and cross-platform binaries
- Add MIT license and trimpath to Makefile
- Add man page generated from markdown via go-md2man
- Fix CI: upgrade to golangci-lint v2 with action v7
- Tighten docs and comments, remove AI-isms
- Add GitHub Actions CI with golangci-lint
- Update CLAUDE.md for library-first architecture
- Restructure hew as an importable library with composable agent API
- Add README
- Add CLAUDE.md for Claude Code context
- Send debug output to stderr, add -v alias, truncate long commands
- Add --verbose flag for debug output
- Add Makefile with help target and gitignore
- Fix review findings: cwd tracking, REPL context, output formatting
- Remove unused AgentConfig, clean up types
- Add CLI with REPL, single-prompt mode, and custom help
- Add agent loop with cwd tracking and step limits
- Apply golang review: rename ParseAction to ExtractCommand, make max_tokens configurable
- Add OpenAI-compatible chat completions adapter
- Add Anthropic Messages API adapter
- Add system prompt with AGENTS.md loading
- Add command executor with timeout and directory support
- Add action parser with fenced bash block extraction
- Scaffold project with types and directory structure
- init

