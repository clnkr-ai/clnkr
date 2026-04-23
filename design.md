# NO_COLOR Monochrome Design

## Architecture

`cmd/clnkr` keeps one startup theme decision: color enabled or `NO_COLOR` monochrome. A small pure helper computes that mode once, `runTUI` builds a `styles` set for chat, status, and input from it, and the same mode goes into markdown rendering.

The monochrome path is subtractive first. It removes ANSI color from Lip Gloss and Glamour output, then adds only a few non-color emphasis choices where existing output would get too flat: warning and error lines, the new-content badge, and input focus. Status stays mostly text-driven because it already emits explicit mode words.

## Components And Boundaries

- `cmd/clnkr/main.go`
  Reads `NO_COLOR` once at TUI startup through a small helper and passes `noColor` into style and renderer setup.
- `cmd/clnkr/styles.go`
  Builds either the existing color palette or a monochrome variant. Preserve the existing `defaultStyles(true)` API for color mode and add a separate startup seam for monochrome so the current test surface stays stable. Monochrome uses `lipgloss.NoColor{}` for foreground, background, and cursor, plus narrow non-color emphasis for the few stateful surfaces that need it.
- `cmd/clnkr/render.go`
  Builds Glamour styles from the same `noColor` input. Color mode keeps the current retro style. No-color mode keeps a small custom monochrome variant: preserve heading prefixes, block quote markers, list markers, code-block framing, and table separators, but remove explicit color and background fields. Renderer caching becomes mode-aware so a cached color renderer cannot bleed into no-color output.
- Existing chat, status, reasoning, input, and UI code
  Consume the same `styles` struct. `chat` and `reasoning` also need explicit markdown mode plumbing because they are the render call sites. No new UI flow, no new mode, no new public flags.

## Data Flow

1. `runTUI` checks `NO_COLOR`.
2. `runTUI` builds `styles` with that result.
3. Chat, status, input, reasoning, and overlays render through those styles as they already do.
4. Markdown rendering receives the same mode and chooses the matching Glamour style config.
5. Tests exercise both modes in-process so renderer reuse bugs show up.

## Failure Handling

- If `NO_COLOR` is unset or empty, behavior stays on the existing color path.
- If the markdown renderer cannot initialize, existing plain-text fallback remains unchanged.
- If monochrome output is too flat in one state, add local non-color emphasis there instead of broad redesign.

## Testing Strategy

- Start with failing tests.
- Add markdown tests that render once in color mode and once in no-color mode in the same process.
- Assert no-color markdown output drops color-setting escapes while preserving the chosen monochrome structure: heading prefixes, block quote markers, code-block framing, and table separators.
- Add style or view-level tests for stateful TUI surfaces that matter here:
  - input cursor path is neutralized
  - warning or error text remains distinct without color
  - new-content badge remains visible without color
- Add one status-level or full `model.View()` no-color assertion that preserves mode text and one-line layout without color-setting escapes.
- Add one composed `model.View()` no-color assertion that uses the same startup style-selection seam as `runTUI`, includes `m.chat.hasNew = true`, and proves chat, status, input, and the new-content badge stay monochrome together.
- Keep existing TUI layout tests passing. Final verification still runs `make _fmt` and `make check`.

## Non-Goals

- No new CLI flag or config file for color control.
- No runtime theme switching after TUI startup.
- No background detection work beyond keeping room for it later.
- No feature work in `cmd/clnku`.
- No broad visual redesign of the TUI.

## Tradeoffs

- A small custom monochrome variant is more code than stock Glamour ASCII, but it better matches the brief because it preserves structure instead of dropping to plain fallback.
- Keying markdown rendering by mode adds a little plumbing, but it prevents stale renderer reuse in tests and future code paths.
- Targeted non-color emphasis is slightly more subjective than pure subtraction. It is still lower risk than leaving important state changes visually flat.
