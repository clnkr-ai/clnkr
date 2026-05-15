---
name: test-intent-ledger
description: Use before adding or reviewing clnkr tests to keep generated tests integration-oriented, behavior-focused, and low-slop.
---

# Test Intent Ledger

Use this before adding or accepting new tests in clnkr.

## Ledger

For each new test or fixture, record:

- Behavior under test: user-visible behavior, protocol contract, or persistence contract.
- Real boundary exercised: CLI, clnkrd JSONL adapter, shell executor, provider adapter, session store, or agent loop.
- Dependency realism: `real`, `fixture`, `fake`, or `mock`.
- Fake/mock reason: why a real dependency or fixture is not suitable.
- Failure proof: command, sabotage patch, or reason fail-before/pass-after is impractical.
- Refactor tolerance: implementation detail that should be able to change without breaking the test.

## clnkr Defaults

- Prefer real shell, temp filesystem, `httptest.Server`, subprocess helpers, and JSONL transcript fixtures over isolated helper tests when practical.
- Keep WET test cases when repeated protocol examples make behavior clearer.
- Use provider fakes only at the model/API boundary; do not fake the behavior being claimed.
- Add helpers only when they remove incidental setup noise, not when they hide behavior.

## Review Rules

Flag tests that:

- assert private implementation shape without checking agent, CLI, provider, or transcript behavior
- duplicate an existing scenario without a new behavior claim
- use fake/mock terminology without explaining why a real boundary is unsuitable
- update protocol fixtures without explaining the user-visible or provider contract that changed
- cannot be made to fail by sabotaging the intended behavior
