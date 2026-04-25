# Evaluations

This directory holds the checked-in evaluation suites for `clnkr`.

Execution is owned by the external `clankerval` runner. Install it from [GitHub Releases](https://github.com/clnkr-ai/clankerval/releases).

The pinned runner version is enforced in [`Makefile`](../Makefile). Tasks now run from the repo root (`"working_directory": "."`) and verify outcomes with diff- and command-based graders instead of checked-in workspace snapshots.

## Layout

```text
evaluations/
  suites/
    <suite-id>/
      suite.json
      tasks/
        <task-id>/
          task.json
          input/
            instruction.txt
            model-turns.json      # mock-provider tasks only
            project/
              AGENTS.md           # optional clnkr prompt file
  trials/
    <trial-id>/
      bundle.json
      raw/
      normalized/
      outcome/
  reports/
    open-test-report.xml
    junit.xml
```

Generated trial bundles live under `evaluations/trials/<trial-id>/`.

Generated run-level reports live under `evaluations/reports/`.

## Task Conventions

- `suite.json` declares the suite id, mode, failure policy, task order, and optional default agent.
- `task.json` declares the instruction file, `working_directory`, step limit, grader config, and optional `scripted_turns_file`.
- `working_directory` must be `"."` with the current runner.
- Mock-provider tasks keep scripted turns in `input/model-turns.json`.
- Live-provider tasks verify outcomes with `outcome_diff`, `outcome_command_output`, and optional transcript grading.

## Bundle Layout

Each trial bundle preserves both `raw/` and `normalized/` records.

- `raw/` keeps exact captured runtime data for debugging and replay-oriented inspection.
- `normalized/` keeps stable comparison/export data for grading, diffing, and report generation.

Current bundle structure:

```text
evaluations/trials/<trial-id>/
  bundle.json
  raw/
    agent/
    commands.jsonl
    provider-requests.jsonl
    provider-responses.jsonl
  normalized/
    transcript.jsonl
    outcome.json
    graders.jsonl
  outcome/
    diff.patch
    name-status.txt
    numstat.txt
```

## Artifact Meanings

- `transcript`: the structured conversation and command lifecycle for a trial.
- `outcome`: the normalized end-state plus git diff artifacts for the trial.
- `grader`: one normalized grading record describing pass/fail status, message, and structured evidence.

Useful maintenance files:

- `raw/agent/`: native agent artifacts such as `trajectory.json` and `events.jsonl`.
- `normalized/transcript.jsonl`: canonicalized transcript records.
- `normalized/outcome.json`: normalized final outcome summary.
- `normalized/graders.jsonl`: one normalized record per enabled grader.
- `raw/commands.jsonl`: exact normalized command records from the run.
- `raw/provider-requests.jsonl`: exact provider requests captured during the run.
- `raw/provider-responses.jsonl`: exact provider responses captured during the run.
- `outcome/diff.patch`: patch-format git diff for the trial workspace.
- `outcome/name-status.txt`: git name-status summary for the trial workspace.
- `outcome/numstat.txt`: git numstat summary for the trial workspace.

## Run

Mock-provider regression suite:

```bash
make evaluations
```

Live-provider evaluation suite:

```bash
make evaluations-live-openai
make evaluations-live-anthropic
```

Both make targets call `clankerval` directly after a `Makefile` preflight that requires `clankerval --version` to match the pinned version exactly.

Canonical CLI examples use `clankerval` directly:

```bash
clankerval run --suite default
CLNKR_EVALUATION_MODE=live-provider clankerval run --suite default
```

The provider-specific make targets are deterministic and then call `make evaluations-live`.

- `make evaluations-live-openai` reads `OPENAI_API_KEY`, optional `CLNKR_EVALUATION_OPENAI_BASE_URL`, and optional `CLNKR_EVALUATION_OPENAI_MODEL`
- `make evaluations-live-anthropic` reads `ANTHROPIC_API_KEY`, optional `CLNKR_EVALUATION_ANTHROPIC_BASE_URL`, and optional `CLNKR_EVALUATION_ANTHROPIC_MODEL`

`make evaluations-live` reads the first-wave runtime configuration from:

- `CLNKR_EVALUATION_MODE`
- `CLNKR_EVALUATION_API_KEY`
- `CLNKR_EVALUATION_BASE_URL`
- `CLNKR_EVALUATION_MODEL`

## Inspect

After a run:

1. Read `evaluations/trials/<trial-id>/bundle.json` to identify the trial and its artifact paths.
2. Inspect `evaluations/trials/<trial-id>/normalized/transcript.jsonl` for canonical transcript flow.
3. Inspect `evaluations/trials/<trial-id>/normalized/outcome.json` for the normalized end-state.
4. Inspect `evaluations/trials/<trial-id>/normalized/graders.jsonl` for required/advisory grading results.
5. Inspect `evaluations/trials/<trial-id>/raw/commands.jsonl` and `evaluations/trials/<trial-id>/raw/agent/` for exact execution details.
6. Inspect `evaluations/trials/<trial-id>/outcome/diff.patch` when you need the concrete workspace diff.

The `evaluations/trials/` and `evaluations/reports/` directories are regenerated per run.
