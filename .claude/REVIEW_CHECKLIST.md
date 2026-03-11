# Binary Symmetry Review Checklist

Use this checklist during code review to ensure hew's two binaries remain synchronized.

## Quick Check (60 seconds)

Before reviewing code, ask:
- [ ] Does this PR touch cmd/hew, cmd/hui, or both?
- [ ] If both, do the changes affect behavior (flags, session save, event handling)?

If yes to both, run: `@review-symmetry`

## Full Checklist (When PR Touches cmd/hew or cmd/hui)

Use this if the skill doesn't catch something, or for manual verification.

### Flags & Environment Variables
- [ ] New flags added to cmd/hew? → Also in cmd/hui?
- [ ] Flag removed from cmd/hew? → Also removed from cmd/hui?
- [ ] Flag default changed? → Same change in both?
- [ ] Env var fallback (e.g., $HEW_MODEL) in cmd/hew? → Also in cmd/hui?

### Session Persistence (Conversational Mode)
- [ ] Auto-save on exit (REPL mode) in cmd/hew? → Also in cmd/hui?
- [ ] Changed session storage logic? → Updated both binaries?
- [ ] `--continue` flag (load session) in cmd/hew? → Also in cmd/hui?
- [ ] `--list-sessions` in cmd/hew? → Also in cmd/hui?

### Event Handling
- [ ] Added event type handling in cmd/hew? → Also in cmd/hui?
- [ ] Removed event handling in cmd/hew? → Also removed in cmd/hui?
- [ ] Changed `agent.Notify` wiring? → Same change in both?
- [ ] All five event types handled: EventResponse, EventCommandStart, EventCommandDone, EventFormatError, EventDebug?

### Error Reporting
- [ ] Changed error messages in cmd/hew? → Consistent in cmd/hui?
- [ ] Changed exit codes? → Same codes in both?
- [ ] Added validation (API key, base URL)? → Both binaries validate?

### Provider Selection (Anthropic vs OpenAI)
- [ ] Changed adapter selection logic? → Identical in both?
- [ ] Added ANTHROPIC_API_KEY fallback? → Both binaries check it?

### Intentional Asymmetries (Document These)
- [ ] TUI-only rendering (color, pagination, keybindings)? → Note why it's TUI-only
- [ ] CLI-only feature (rare)? → Document justification in commit message
- [ ] Feature in one binary, not the other? → Ensure it's documented and approved

## Red Flags (Probably a Bug)

Stop and ask for fixes if:
- [ ] New flag in cmd/hew but not cmd/hui (unless documented as intentional)
- [ ] Session persistence in cmd/hew but not cmd/hui
- [ ] Event type handled differently in the two binaries
- [ ] Error exit codes differ between binaries
- [ ] Provider selection logic differs

## Green Flags (Intentional, OK)

These are fine:
- [ ] TUI rendering differs (color, layout, keybindings) but behavior is same
- [ ] Variable names differ (eventLog vs eventLogPath) but behavior is same
- [ ] Code structure differs (TUI uses bubbletea, CLI uses bufio) but capabilities are same

## Questions for the Author

If unsure, ask:

1. "I see --foo-bar in cmd/hew but not cmd/hui. Is this intentional or a miss?"
2. "Did you update both binaries when you changed the session save logic?"
3. "Run `/review-symmetry` to double-check binary parity?"

## Approval Gates

**Don't approve unless:**
- [ ] All symmetric behaviors are present in both binaries
- [ ] All intentional asymmetries are documented and justified
- [ ] `/review-symmetry` skill output (if run) shows "OK" on all behaviors
- [ ] If unsure, ask the author or run the skill

**Do approve if:**
- [ ] Behavioral parity verified (flags, session, events, errors, providers)
- [ ] Only intentional asymmetries (TUI rendering, documented CLI-only features)
- [ ] No red flags detected

## Tips

- **Trust the skill**: `/review-symmetry` is more reliable than manual inspection. Run it.
- **Focus on behavior**: Don't worry if cmd/hew and cmd/hui have different code structure. Care about whether both *do the thing*.
- **Test conversational mode**: Bugs hide in session auto-save and REPL mode more than single-task mode.
- **Ask, don't assume**: If you're unsure whether something is intentional, ask. Better to clarify than to approve a bug.

## Example: PR with Flag Addition

PR adds `--sandbox` flag to prevent destructive operations.

**Your check:**
1. Does PR touch cmd/hew? Yes (--sandbox parsing added).
2. Does PR touch cmd/hui? Need to verify.
3. Run: `@review-symmetry`

**Skill output:**
```
| Behavior | cmd/hew | cmd/hui | Status |
|----------|---------|---------|--------|
| --sandbox flag | ✓ | ✗ | BUG: Missing in cmd/hui |
| Flag used to gating operations | ✓ | ✗ | BUG: hui doesn't check flag |
```

**Your action:**
Request fix: "Add --sandbox flag parsing and gating logic to cmd/hui before merging."

## Example: PR with TUI Rendering Change

PR improves TUI output colors.

**Your check:**
1. Does PR touch cmd/hew? No.
2. Does PR touch cmd/hui? Yes (rendering only).
3. Run: `@review-symmetry`

**Skill output:**
```
| Behavior | cmd/hew | cmd/hui | Status |
|----------|---------|---------|--------|
| Event rendering | ✓ Plain text | ✓ Colored text | OK (intentional) |
| All events still fired | ✓ Yes | ✓ Yes | OK |
```

**Your action:**
Approve: "TUI rendering change is intentional and maintains behavioral parity."
