---
name: focused-sabotage-testing
description: Use after adding or changing clnkr tests to prove the tests fail when the intended behavior is deliberately broken.
---

# Focused Sabotage Testing

Use this for behavior changes where a new test could pass trivially or mirror
the current implementation.

## Workflow

1. Identify the smallest deliberate code break for the behavior under test.
2. Save it as a patch outside the repo, usually under `/tmp`.
3. Run:

   ```sh
   scripts/sabotage-check.sh --patch /tmp/clnkr-sabotage.patch --test-cmd 'make test'
   ```

4. If the sabotage patch does not make the test fail, improve the test before
   accepting it.
5. Keep the sabotage patch out of git.

## Patch Rules

- Break behavior, not compilation.
- Change one thing.
- Prefer public CLI output, provider request/response shape, typed event output, transcript persistence, or shell execution behavior.
- Do not sabotage unrelated error handling just to force a failure.

## Evidence

In review notes, record:

- patch intent
- exact test command
- failing result under sabotage
- passing result after restore
