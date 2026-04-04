---
name: clnkr-long-running-child-reviews
description: |
  Runs and harvests long-lived child clnku review jobs when inline execution is
  likely to hit host time limits. Use when: (1) child reviews read substantial repo
  context, (2) synchronous waiting keeps getting interrupted, (3) output must be
  recovered from files instead of an inline command result.
author: Claude Code
version: 1.0.0
date: 2026-04-01
---

# Long-running Child clnku Reviews

## When to Use

- A child `clnku -p` review is likely to exceed host inline command limits
- Synchronous waits have already returned interrupted or timed out
- You need a child to read large context and produce a review or design critique
- You can rendezvous through output files and trajectory files

## When NOT to Use

- The child task is small enough to complete inline
- You only need a short grep/read result that the parent can gather directly
- The task output does not need to survive host interruption

## Problem

Inline child `clnku` runs are vulnerable to host timeouts and interruption, especially when they need to read a design doc plus surrounding repo context. The non-obvious part is that the child may still be healthy; the host only lost patience with the synchronous wait. You need a file-based handoff instead of assuming the child failed.

## Solution

### Step 1: Launch detached with output files

When inline waits are already failing, start the child with `nohup` and redirect stdout/stderr to a file.

Pattern:

```bash
nohup clnku -p "...review prompt..." --trajectory /tmp/review.json --full-send > /tmp/review.out 2>&1 &
```

Capture the PID so you can inspect whether it is still alive.

### Step 2: Use files as the rendezvous point

Do not wait inline after detaching.

Instead inspect:

- the redirected output file for visible progress or final output
- the trajectory file for the actual conversation/result
- the process table to see whether the child is still running

### Step 3: Distinguish timeout from approval-mode failure

A child stopping because it asked for approval is a different problem from host interruption.

- Missing `--full-send` can leave the child blocked at the approval prompt.
- Host interruption after `--full-send` means the child may still be working fine.

Fix the first by rerunning with `--full-send`.
Fix the second by switching to detached/file-based monitoring.

### Step 4: Poll conservatively

When checking long-running children or external release workflows, back off between checks instead of tight-loop polling.

A 10-second cadence is a reasonable default once the job is known to be running.

### Step 5: Recover final output from trajectory when possible

The redirected output file may be empty or only show intermediate reads.

The more reliable final result is often the trajectory file, once it exists and closes cleanly.

If the trajectory is missing and the process is still alive, the right conclusion is usually “still running”, not “failed”.

## Verification

1. Confirm the detached child PID exists after launch.
2. Check whether the output file or trajectory file appears.
3. If the process is still running and files are incomplete, continue polling conservatively.
4. Once complete, read the trajectory file first for the final result.
5. Only treat it as failed if the process exits and the files show an actual error.

## References

- `clnku --help`
- host command timeout behavior from this environment
