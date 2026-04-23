# NO_COLOR Monochrome Implementation Plan

**Goal:** Honor `NO_COLOR` in `cmd/clnkr`, remove ANSI color from TUI and markdown output, and keep important states legible with minimal monochrome emphasis.

**Ordered tasks**

1. Add red tests for a pure `NO_COLOR` detection helper, a pure startup style-selection seam, markdown mode switching, status-bar no-color rendering, and one composed `model.View()` no-color path with `hasNew` enabled.
   Targets: `cmd/clnkr/render_test.go`, `cmd/clnkr/main_test.go` or a new focused test file, `cmd/clnkr/status_test.go`, `cmd/clnkr/ui_test.go`
   Verification: `cd cmd/clnkr && go test ./... -run 'TestRenderMarkdown|Test.*NoColor|TestStatus.*NoColor|TestUI.*NoColor'`

2. Add a small pure helper for startup-only `NO_COLOR` detection and a pure startup style-selection seam, then wire them into the TUI path.
   Targets: `cmd/clnkr/main.go`
   Verification: focused tests for `noColorEnabled(getenv)` and the startup style-selection seam.

3. Preserve the existing color-style API and add a separate monochrome style builder used only by startup theme selection.
   Targets: `cmd/clnkr/styles.go`
   Verification: focused tests for monochrome style outputs plus the existing module test suite to catch unchanged direct callers.

4. Neutralize the dedicated input cursor color path and add only the minimum non-color emphasis needed for focus and state changes.
   Targets: `cmd/clnkr/styles.go`, `cmd/clnkr/input.go` if needed, `cmd/clnkr/ui.go`
   Verification: focused tests covering cursor styling, warnings or errors, and new-content badge visibility through `model.View()`.

5. Make markdown rendering no-color aware with a small custom monochrome variant, thread mode explicitly through `chat` and `reasoning`, and fix the renderer cache so mode changes in one process rebuild correctly.
   Targets: `cmd/clnkr/render.go`, `cmd/clnkr/reasoning.go`, `cmd/clnkr/chat.go`
   Verification: focused markdown tests that render color then no-color in the same process and assert preserved monochrome structure: heading prefixes, block quote markers, code-block framing, and table separators.

6. Run module-level verification, then repo-level verification.
   Verification:
   - `cd cmd/clnkr && go test ./...`
   - `make _fmt`
   - `make check`

**File and module targets**

- `cmd/clnkr/main.go`
- `cmd/clnkr/styles.go`
- `cmd/clnkr/render.go`
- `cmd/clnkr/chat.go`
- `cmd/clnkr/reasoning.go`
- `cmd/clnkr/ui.go`
- `cmd/clnkr/render_test.go`
- `cmd/clnkr/main_test.go` or new no-color-focused tests
- `cmd/clnkr/status_test.go`
- `cmd/clnkr/ui_test.go`
- Possibly `cmd/clnkr/input_test.go`, `cmd/clnkr/chat_test.go`, `cmd/clnkr/reasoning_test.go`, `cmd/clnkr/delegate_chat_test.go`, or `cmd/clnkr/program_integration_test.go` if the startup seam or style builder changes require mechanical updates

**Risks and rollback notes**

- Risk: monochrome output becomes too flat.
  Rollback note: keep changes local to style builders and markdown style config so emphasis can be reduced or removed without touching core TUI flow.
- Risk: renderer cache keeps stale color mode.
  Rollback note: make cache invalidation explicit and covered by same-process tests before final verification.
- Risk: style constructor changes ripple through many tests.
  Rollback note: keep the public builder shape simple and update tests mechanically where possible.

**Explicit assumptions**

- `NO_COLOR` only affects `cmd/clnkr` in this feature.
- Startup-only detection is sufficient.
- A pure helper such as `noColorEnabled(getenv func(string) string) bool` is an acceptable seam for testing startup behavior.
- A pure startup style-selection seam is preferable to changing the signature of `defaultStyles(true)` because many existing tests call it directly.
- Non-color emphasis like bold, reverse, underline, prefixes, and borders is allowed in monochrome mode.
- Markdown no-color output keeps a named custom monochrome structure rather than dropping all the way to stock Glamour ASCII defaults.
- Status bar readability is mostly a verification concern because mode labels are already explicit text.
- One composed startup-seam test is enough proof that monochrome styles reach the assembled TUI without needing to run Bubble Tea itself in the test.
