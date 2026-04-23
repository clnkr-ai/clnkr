# Problem statement

`cmd/clnkr` always builds a colorized retro theme for the TUI and a colorized Glamour markdown renderer. Verified requirement from the feature brief: “honor `NO_COLOR`,” “preserve bold, spacing, borders, labels, and explicit mode text,” “ensure focused/active/error state remains legible without color,” and “make markdown rendering switch to a no-color style config” (`/home/bcosgrove/tmp/tui-multiagent-coord/clnkr-03-no-color-monochrome.md:17-21`). That is narrower than “disable styling,” and it matches the current code paths that hard-code color in both Lip Gloss and Glamour (`cmd/clnkr/styles.go:56-98`, `cmd/clnkr/render.go:14-205`).

## Current repo constraints and relevant code paths

- `cmd/clnkr` is its own Go module with Charm dependencies isolated there, not in the root module (`cmd/clnkr/go.mod:1-15`; `AGENTS.md` “Architecture” and “Rules”).
- Repository guidance explicitly says `cmd/clnkr` does not need feature parity with `cmd/clnku`, so `NO_COLOR` can be implemented only in the TUI without changing the plain CLI (`AGENTS.md` “Rules”).
- `runTUI` always calls `defaultStyles(true)` and never reads `NO_COLOR`; the `true` argument is hard-coded and annotated with `TODO: detect actual background` (`cmd/clnkr/main.go:496-497`).
- `defaultStyles` ignores its `hasDark` parameter and constructs every chat, status, and input style from explicit hex colors, including foreground, background, cursor, and error accents (`cmd/clnkr/styles.go:56-98`).
- UI state visibility depends on those styles in a few concentrated places:
  - chat command pending/success/error/debug/warning/new-content indicator (`cmd/clnkr/chat.go:90-145`, `cmd/clnkr/ui.go:1053-1061`)
  - status bar mode/hints line (`cmd/clnkr/status.go:58-80`, `cmd/clnkr/ui.go:1072-1111`)
  - textarea prompt/text/placeholder/cursor-line/cursor wiring, including a dedicated cursor color path (`cmd/clnkr/input.go:18-45`, `cmd/clnkr/styles.go:35-47,91-95`)
- Markdown rendering is also globally colorized. `initRenderer` caches one shared Glamour `TermRenderer` built with `glamour.WithStyles(retroMarkdownStyle())` and `glamour.WithWordWrap(width)` (`cmd/clnkr/render.go:9-24`). `retroMarkdownStyle` starts from `glamourstyles.ASCIIStyleConfig` but then reintroduces explicit colors and backgrounds for headings, links, code, code blocks, tables, and block quotes (`cmd/clnkr/render.go:42-205`).
- Renderer cache invalidation is width-driven only. `chat.resize` and `reasoning.rendered` clear the package-global `renderer`, but there is no theme key in that cache today. A startup-only implementation can still avoid stale renders by keying the cache on `noColor` state or by rebuilding when the requested mode differs from the cached mode (`cmd/clnkr/chat.go:293-298`, `cmd/clnkr/reasoning.go:50-53`, `cmd/clnkr/render.go:9-31`).
- Existing tests cover normal rendering and layout, but nothing in `cmd/clnkr` asserts `NO_COLOR` behavior today:
  - `render_test.go` checks only “transforms markdown,” code-block preservation, empty input, and width change (`cmd/clnkr/render_test.go:8-50`)
  - many tests construct `defaultStyles(true)` directly, so a signature or behavior change will ripple across the TUI tests (`cmd/clnkr/ui_test.go:18-30`, `cmd/clnkr/status_test.go:11-16`, `cmd/clnkr/input_test.go:8-18`)
- Verified baseline: `go test ./...` passes in `cmd/clnkr` in this worktree (`cmd/clnkr`: local command run on 2026-04-23, output `ok github.com/clnkr-ai/clnkr/cmd/clnkr 0.954s`).

## External constraints or APIs

- `NO_COLOR` convention: command-line software that adds ANSI color by default should check `NO_COLOR`; if the variable is present and not empty, ANSI color should be prevented regardless of value. User-level override example is `NO_COLOR=1` (`https://no-color.org/`, lines 7-10).
- Lip Gloss exposes `NoColor`, defined as “absence of color styling”; with it, foreground uses terminal default text color and background is not drawn (`/home/bcosgrove/go/pkg/mod/charm.land/lipgloss/v2@v2.0.1/color.go:45-60`; also `https://pkg.go.dev/charm.land/lipgloss/v2`, lines 2327-2340).
- Lip Gloss `Style.Foreground` and `Style.Background` accept `color.Color`, so monochrome styles can stay in the same API surface by swapping color values rather than replacing the rendering stack (`/home/bcosgrove/go/pkg/mod/charm.land/lipgloss/v2@v2.0.1/set.go:272-280`).
- Lip Gloss docs recommend Bubble Tea apps detect terminal background via `tea.BackgroundColorMsg`, while standalone usage can call `lipgloss.HasDarkBackground`; current `cmd/clnkr` does neither (`https://pkg.go.dev/charm.land/lipgloss/v2`, lines 1110-1144 and 1703-1719; `cmd/clnkr/main.go:496-497`).
- Glamour supports swapping markdown style configs directly through `glamour.WithStyles(styles ansi.StyleConfig)` and keeping wrapping via `glamour.WithWordWrap(width)` (`/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/glamour.go:163-195`; also `https://pkg.go.dev/github.com/charmbracelet/glamour`, lines 435-461).
- Glamour ships `ASCIIStyleConfig`, documented as using only ASCII characters, and `NoTTYStyleConfig` is an alias of that config (`/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/styles/styles.go:29-90,662-670`; also `https://pkg.go.dev/github.com/charmbracelet/glamour/styles`, lines 1339-1348).

## Viable approaches with tradeoffs

### 1. Add explicit `noColor` theme branching in `cmd/clnkr`

Build styles from a small theme decision at startup, pass that through to both Lip Gloss styles and the markdown renderer, and keep existing layout/state strings unchanged. In the no-color branch, swap foreground/background uses to `lipgloss.NoColor{}` and use non-color attributes already supported by Lip Gloss and Glamour, such as `Bold`, `Faint`, `Underline`, prefixes, ASCII separators, and existing icons (`cmd/clnkr/styles.go:18-98`; `/home/bcosgrove/go/pkg/mod/charm.land/lipgloss/v2@v2.0.1/color.go:45-60`; `/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/styles/styles.go:29-90`).

Tradeoff: lowest product risk if focused on the explicit brief requirements and kept startup-only for now, which matches the current `runTUI` lifecycle (`cmd/clnkr/main.go:496-497`). It still needs one explicit renderer-cache rule so a color renderer cannot be reused when `NO_COLOR` is requested later in the same process.

### 2. Reuse Glamour ASCII/NoTTY for markdown and keep a custom monochrome Lip Gloss palette

For markdown only, switch `retroMarkdownStyle()` to return `glamourstyles.NoTTYStyleConfig` or `ASCIIStyleConfig` when `NO_COLOR` is active, while separately designing TUI monochrome Lip Gloss styles for status/input/chat (`cmd/clnkr/render.go:42-205`; `/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/styles/styles.go:29-90,662-670`).

Tradeoff: simplest markdown path and closest match to upstream no-color behavior, but stock ASCII Glamour will not preserve the current retro visual accents. Example: current H1 uses inverted foreground/background banner styling, while ASCII falls back to textual heading prefixes (`cmd/clnkr/render.go:66-74`; `/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/styles/styles.go:57-85`). That may undershoot the brief’s “intentional monochrome pass.”

### 3. Keep one renderer/style builder API that accepts both `hasDark` and `noColor`

Refactor `defaultStyles(hasDark bool)` and markdown style creation into paired builders that accept explicit runtime theme inputs, then initialize them from Bubble Tea background-color detection plus `NO_COLOR` precedence. This addresses the existing `TODO` and prevents another round of theme plumbing later (`cmd/clnkr/styles.go:56-57`; `cmd/clnkr/main.go:496-497`; `https://pkg.go.dev/charm.land/lipgloss/v2`, lines 1118-1130).

Tradeoff: best long-term shape, but more scope than the feature strictly needs. It touches startup behavior and likely requires new tests for background detection plumbing in addition to `NO_COLOR`.

### 4. Minimal env gate only

Check `NO_COLOR`, set Lip Gloss colors to `NoColor`, and remove Glamour color fields without otherwise redesigning states (`/home/bcosgrove/go/pkg/mod/charm.land/lipgloss/v2@v2.0.1/color.go:45-60`; `cmd/clnkr/styles.go:67-96`; `cmd/clnkr/render.go:42-205`).

Tradeoff: smallest patch, but highest UX risk. Current error, new-content, cursor-line, and cursor styling rely on color contrast, so a purely subtractive pass may flatten important distinctions or leave one remaining color path behind (`cmd/clnkr/styles.go:78-96`; `cmd/clnkr/ui.go:1055-1065`; `cmd/clnkr/input.go:29-45`).

## Open questions

- `NO_COLOR` should take precedence over automatic background detection for this feature. That matches the external convention for default color output and keeps startup behavior deterministic. A future explicit `--color` or config override could supersede it (`https://no-color.org/`, lines 68-75; `cmd/clnkr/main.go:496-497`).
- Should monochrome mode use reverse/underline/bold for the input cursor line, warnings, success/error lines, and new-content badge after color subtraction? Those are the surfaces where current code still depends most on visual contrast (`cmd/clnkr/styles.go:67-96`; `cmd/clnkr/chat.go:90-145`; `cmd/clnkr/input.go:29-45`).
- Is the desired markdown no-color output closer to upstream ASCII/NoTTY, or to a custom repo-specific monochrome style that preserves some retro structure without ANSI colors? The brief requires an intentional monochrome pass, but it does not require preserving every existing retro accent (`/home/bcosgrove/tmp/tui-multiagent-coord/clnkr-03-no-color-monochrome.md:13-21`).
- Verification should assert output behavior, not only builder-path selection. In `NO_COLOR` mode, tests should reject color-setting escape sequences while allowing intentional non-color emphasis such as bold, underline, reverse, spacing, prefixes, and ASCII structure (`cmd/clnkr/render_test.go:8-50`; `https://no-color.org/`, lines 7-10).

## Verified facts

- `cmd/clnkr` is a separate Go module and imports `bubbletea`, `bubbles`, `lipgloss/v2`, and `glamour` there (`cmd/clnkr/go.mod:1-15`).
- `runTUI` hard-codes `defaultStyles(true)` and does not consult `NO_COLOR` (`cmd/clnkr/main.go:496-497`).
- `defaultStyles` ignores `hasDark` and creates a fully colorized palette for chat, status, and input (`cmd/clnkr/styles.go:56-98`).
- Chat rendering applies styled pending/success/error/debug/warning lines and a styled “new content” indicator (`cmd/clnkr/chat.go:90-145`; `cmd/clnkr/ui.go:1055-1061`).
- Status mode text is plain text content rendered through a single `Bar` style; modes include `HELP`, `DIFF`, `REASONING`, `APPROVAL`, `CLARIFY`, `RUNNING`, `SCROLL`, and `INPUT` (`cmd/clnkr/status.go:58-80`; `cmd/clnkr/ui.go:1072-1111`).
- Markdown rendering uses a shared cached Glamour renderer configured with `WithStyles(retroMarkdownStyle())` and `WithWordWrap(width)` (`cmd/clnkr/render.go:9-24`).
- `retroMarkdownStyle` overrides ASCII base styles with explicit colors/backgrounds for headings, strong/emphasis, rules, lists, links, inline code, code blocks, and tables (`cmd/clnkr/render.go:42-205`).
- Lip Gloss `NoColor` exists specifically to remove color styling while leaving style APIs intact (`/home/bcosgrove/go/pkg/mod/charm.land/lipgloss/v2@v2.0.1/color.go:45-60`).
- Glamour exposes API support for swapping style configs and keeping word wrapping unchanged (`/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/glamour.go:163-195`).
- Glamour ships ASCII/NoTTY built-ins that can serve as a no-color markdown baseline (`/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/styles/styles.go:29-90,662-670`).
- `go test ./...` passes in `cmd/clnkr` in this worktree (`cmd/clnkr`: local command run on 2026-04-23, output `ok github.com/clnkr-ai/clnkr/cmd/clnkr 0.954s`).

## Inference

- Most defensible starting point is a narrow version of approach 1: explicit `NO_COLOR` plumbing plus the smallest monochrome style changes needed to satisfy the brief. The code already carries semantic text and icons for status and chat states, so the implementation should start with subtraction of color and add non-color emphasis only where rendered output shows a real readability loss (`cmd/clnkr/status.go:58-80`; `cmd/clnkr/chat.go:90-145`; `cmd/clnkr/ui.go:1055-1065`).
- Tests should check rendered output for the absence of color-setting escapes in `NO_COLOR` mode, while still allowing intentional non-color emphasis. Builder-path tests alone would miss regressions in the actual terminal output.
- Input cursor color is part of the feature scope because it bypasses Lip Gloss text/background styling and is wired separately through `inputModel` (`cmd/clnkr/styles.go:41,94`; `cmd/clnkr/input.go:44`).
- Renderer caching needs explicit no-color awareness in code or tests, even for a startup-only feature, because the current package-global renderer is reused until manually cleared (`cmd/clnkr/render.go:9-31`).
- If the implementation also addresses background detection now, Bubble Tea’s `BackgroundColorMsg` path is the upstream-aligned way to do it, but that is optional for `NO_COLOR` itself (`https://pkg.go.dev/charm.land/lipgloss/v2`, lines 1118-1130 and 1703-1719).

## Citations

- Repo docs: `AGENTS.md`
- Repo code:
  - `cmd/clnkr/go.mod:1-15`
  - `cmd/clnkr/main.go:496-497`
  - `cmd/clnkr/styles.go:18-103`
  - `cmd/clnkr/render.go:9-218`
  - `cmd/clnkr/chat.go:77-298`
  - `cmd/clnkr/status.go:11-132`
  - `cmd/clnkr/input.go:18-45`
  - `cmd/clnkr/ui.go:1036-1111`
  - `cmd/clnkr/render_test.go:8-50`
  - `cmd/clnkr/status_test.go:11-127`
  - `cmd/clnkr/ui_test.go:18-30`
- Local dependency source:
  - `/home/bcosgrove/go/pkg/mod/charm.land/lipgloss/v2@v2.0.1/color.go:45-60`
  - `/home/bcosgrove/go/pkg/mod/charm.land/lipgloss/v2@v2.0.1/set.go:272-280`
  - `/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/glamour.go:163-195`
  - `/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/styles/styles.go:29-90`
  - `/home/bcosgrove/go/pkg/mod/github.com/charmbracelet/glamour@v0.10.0/styles/styles.go:662-670`
- External docs:
  - `https://no-color.org/` lines 7-10
  - `https://pkg.go.dev/charm.land/lipgloss/v2` lines 1110-1144, 1703-1719, 2327-2340
  - `https://pkg.go.dev/github.com/charmbracelet/glamour` lines 435-461
  - `https://pkg.go.dev/github.com/charmbracelet/glamour/styles` lines 1339-1348
