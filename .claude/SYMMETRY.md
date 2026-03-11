# Binary Symmetry Prevention Guide

This guide explains the multi-layered system for preventing symmetric bugs in hew's two binaries (cmd/hew and cmd/hui).

## The Problem

hew ships two binaries with different implementations but identical user-facing behavior:

- **cmd/hew**: Plain CLI (stdlib only)
- **cmd/hui**: TUI frontend with bubbletea (detects non-TTY, falls back to plain-text)

When a feature is added to one binary but not the other, it breaks user workflows. Example: hui was missing auto-save on exit, breaking CI scripts that used `--continue`.

## The Solution: Layered Enforcement

Three complementary mechanisms catch symmetric bugs at different stages:

### Layer 1: CLAUDE.md Reference (Passive Awareness)

**Location**: `/home/admin/code/hew/CLAUDE.md`, section "Binary Symmetry Requirements"

**What it does**: Documents all required behaviors, intentional asymmetries, and checking process.

**Who reads it**: Every agent working on hew. All prompt-based tools inherit this guidance.

**When it catches bugs**: Before coding (agents should read it) and during code review (reviewers see the checklist).

### Layer 2: Pre-commit Hook (Local Safety Net)

**Location**: `/home/admin/code/hew/.githooks/pre-commit`

**What it does**: Runs before `git commit`. Detects if one binary has a flag the other lacks.

**Who runs it**: Automatically on your local machine (if `make setup` was run).

**Exit behavior**:
- If flag asymmetry detected → warns and exits 1 (blocks commit)
- If flags are symmetric → allows commit

**When it catches bugs**: Before pushing to GitHub, giving you a chance to fix locally.

**Limitations**: Detects flag declarations only. Doesn't verify behaviors are implemented correctly (that's the skill's job).

### Layer 3: review-symmetry Skill (Comprehensive Audit)

**Location**: `/home/admin/code/hew/.claude/skills/review-symmetry/SKILL.md`

**What it does**: Runs during code review. Audits all behavioral differences and flags bugs vs. intentional asymmetries.

**Who runs it**: Code reviewers invoke `@review-symmetry` before approving a PR.

**Process**:
1. Fetches PR diff
2. Enumerates changes in both binaries
3. Cross-checks five behavioral areas: flags, session persistence, event handling, error reporting, provider selection
4. Reports bugs (blocking), intentional asymmetries (OK), and gaps

**When it catches bugs**: Before merge, with detailed explanation of what's missing.

**Scope**: Behavioral parity, not syntax. Distinguishes bugs from TUI-specific rendering.

### Layer 4: GitHub Actions (CI Validation)

**Location**: `/home/admin/code/hew/.github/workflows/symmetry-check.yml`

**What it does**: Runs on every PR to main. Detects flag and session operation asymmetries.

**Who runs it**: Automatically on GitHub (no user action needed).

**Exit behavior**:
- If asymmetries found → marks PR as failing checks
- If symmetric → marks as passing

**When it catches bugs**: Even if the local hook was bypassed (e.g., `git commit --no-verify`).

## Workflow: Adding a Feature

You decide to add `--auto-cleanup` to hew. Here's how the layers work together:

### Step 1: Code (Before Committing)

You add the flag to `cmd/hew/main.go`:
```go
autoCleanup := flags.Bool("auto-cleanup", false, "")
```

You're about to commit. You run `make setup` (if not already done) and commit:
```bash
git add cmd/hew/main.go
git commit -m "Add auto-cleanup flag"
```

**Layer 1 (CLAUDE.md)**: You may have read it during planning and remembered: "both binaries must have the same flags."

**Layer 2 (Pre-commit Hook)**: Hook runs. It detects:
- cmd/hew has `--auto-cleanup`
- cmd/hui doesn't

Hook exits 1, blocking the commit:
```
⚠ Binary flag asymmetry detected!
  In cmd/hew but not cmd/hui: auto-cleanup
```

You fix it by adding the flag to `cmd/hui/main.go` too.

### Step 2: Code Review

You push the branch and create a PR. You ask a colleague to review.

**Layer 3 (review-symmetry Skill)**: Reviewer runs:
```
@review-symmetry
```

Skill audits the PR:
1. Finds `--auto-cleanup` flag in both binaries ✓
2. Checks if both binaries *use* the flag (read the value and act on it)
3. Reports: "Flags are symmetric. Both binaries parse and implement auto-cleanup."

### Step 3: Merge

Reviewer approves. PR is ready to merge.

**Layer 4 (GitHub Actions)**: CI runs `symmetry-check.yml`:
1. Extracts flags from both binaries
2. Compares: both have `--auto-cleanup` ✓
3. Checks session operations: no asymmetries ✓
4. Marks PR as passing

Merge succeeds. Your feature is in both binaries.

## When Layers Catch Bugs

### Scenario: You Forget to Update One Binary

You add `--auto-cleanup` to cmd/hew but forget cmd/hui.

**Layer 2 (Pre-commit Hook)** catches it:
```
⚠ Binary flag asymmetry detected!
  In cmd/hew but not cmd/hui: auto-cleanup
```

You fix it locally before pushing. Problem solved.

### Scenario: Hook is Bypassed

You commit locally without running `make setup`, or use `git commit --no-verify`:
```bash
git commit --no-verify -m "Add auto-cleanup"
git push
```

Hook doesn't run. PR is created.

**Layer 4 (GitHub Actions)** catches it:
- Workflow runs on PR
- Detects flag asymmetry
- Marks PR as failing checks
- Blocks merge (configured in branch protection)

Reviewer sees failing check and comments: "Flag asymmetry detected. Add --auto-cleanup to cmd/hui."

### Scenario: Flag Exists But Behavior is Wrong

You add `--auto-cleanup` to both binaries but only cmd/hew actually *implements* it (cmd/hui parses it but ignores it).

**Layer 2 (Pre-commit Hook)**: Passes (both have the flag).

**Layer 4 (GitHub Actions)**: Passes (both declare the flag).

**Layer 3 (review-symmetry Skill)**: Reviewer runs it. Skill audits:
```markdown
| Behavior | cmd/hew | cmd/hui | Status |
|----------|---------|---------|--------|
| --auto-cleanup flag | ✓ | ✓ | OK |
| auto-cleanup implementation | ✓ Calls cleanup() | ✗ Flag ignored | BUG |
```

Reviewer sees the bug, requests fix before approving.

## Using the Skill: @review-symmetry

### Quick Start

During code review, invoke:
```
@review-symmetry
```

Skill asks: "Which PR or branch?" You answer: "feature/auto-cleanup" or "PR #5"

Skill runs a comprehensive audit and reports findings.

### Manual Invocation (Advanced)

You can also run it standalone with a custom branch:

Skill will:
1. Check out the branch
2. Diff against main
3. Audit all five behavioral areas
4. Report results

## Intentional Asymmetries (These Are OK)

Some differences are intentional and documented:

### TUI-Specific Rendering
- cmd/hui uses color, pagination, keybindings
- cmd/hew uses plain text, line-by-line output
- **Both have identical event handling**: behavior is the same, rendering differs

### CLI-Only Behavior (Rare)
- If one binary has a feature the other lacks, it must be:
  - Justified in the commit message
  - Documented in CLAUDE.md
  - Approved in code review

Example: `--list-sessions` is in both binaries (symmetric). But if you added a TUI-specific keybinding (e.g., "Press 'l' to list sessions"), that's OK—it's TUI-only UI.

## Enforcement Recap

| Layer | Mechanism | When | Scope | Who |
|-------|-----------|------|-------|-----|
| 1 | CLAUDE.md | Before/during coding | Awareness + checklist | Developers, reviewers |
| 2 | Pre-commit hook | Before commit (local) | Flag declarations | Git (automatic) |
| 3 | review-symmetry skill | During PR review | All behaviors | Reviewers (manual) |
| 4 | GitHub Actions | Before merge (CI) | Flag declarations + session ops | GitHub (automatic) |

**Together, these layers ensure:**
- Symmetric bugs are hard to introduce
- When they slip through, they're caught before merge
- Reviews are systematic, not ad-hoc
- CI validates even if local checks are bypassed

## Troubleshooting

### Hook Doesn't Run on Commit

If you committed without the hook running:

1. Did you run `make setup`? It configures Git to use `.githooks/`:
   ```bash
   cd /home/admin/code/hew
   make setup
   ```

2. Verify the hook is executable:
   ```bash
   ls -la .githooks/pre-commit
   ```
   Should show `rwxr-xr-x` (755 permissions).

3. Test it manually:
   ```bash
   .githooks/pre-commit
   ```

### Hook Gives False Positives

The hook detects flag declarations only. It may warn if:
- A flag was renamed but both binaries still declare the old name
- A flag is intentionally asymmetric (CLI-only, TUI-only)

If this happens, document the exception in a commit message:
```
Add --tui-specific flag

This flag is intentional in cmd/hui only because [reason].
Disable hook warning for this PR.
```

Then bypass the hook for that commit:
```bash
git commit --no-verify
```

### Skill Gives Unclear Results

If `/review-symmetry` is confusing or reports something unclear:

1. Re-run it with more context: Point it to a specific file or behavior
2. Ask it to clarify: "Is this a bug or intentional?"
3. Check CLAUDE.md: See if the behavior is documented as intentional

## When to Ignore Warnings

Generally: don't. Symmetric bugs are sneaky.

**Exception**: If you have a very good reason for asymmetry:
1. Document it in the commit message
2. Update CLAUDE.md "Intentional Asymmetries" section
3. Get explicit approval in code review
4. Use `git commit --no-verify` if needed (sparingly)

## Questions?

Refer to CLAUDE.md section "Binary Symmetry Requirements" for:
- Full list of required symmetric behaviors
- Detailed checklist for reviewers
- Examples of intentional asymmetries
