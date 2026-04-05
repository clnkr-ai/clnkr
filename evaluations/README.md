# Evaluations

This directory holds the checked-in evaluation suites for `clnku`.

Execution is owned by the external `clankerval` runner. Install it separately with `./scripts/install-clankerval.sh`. `clnkeval` remains a compatibility alias to the same standalone runner.

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
          expected/
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

Task inputs live under `evaluations/suites/<suite-id>/tasks/<task-id>/input/`.

Expected task outputs live under `evaluations/suites/<suite-id>/tasks/<task-id>/expected/`.

Generated trial bundles live under `evaluations/trials/<trial-id>/`.

Generated run-level reports live under `evaluations/reports/`.

## Bundle Layout

Each trial bundle preserves both `raw/` and `normalized/` records.

- `raw/` keeps exact captured runtime data for debugging and replay-oriented inspection.
- `normalized/` keeps stable comparison/export data for grading, diffing, and report generation.

Current bundle structure:

```text
evaluations/trials/<trial-id>/
  bundle.json
  raw/
    transcript.json
    events.jsonl
    provider-requests.jsonl
    provider-responses.jsonl
  normalized/
    transcript.jsonl
    outcome.json
    graders.jsonl
  outcome/
    workspace/
```

## Artifact Meanings

- `transcript`: the structured conversation and command lifecycle for a trial.
- `outcome`: the final end-state, including the materialized workspace snapshot.
- `grader`: one normalized grading record describing pass/fail status, message, and structured evidence.

Useful maintenance files:

- `normalized/transcript.jsonl`: canonicalized transcript records.
- `normalized/outcome.json`: normalized final outcome summary.
- `normalized/graders.jsonl`: one normalized record per enabled grader.
- `raw/events.jsonl`: exact command lifecycle events from the run.

## Run

Mock-provider regression suite:

```bash
make evaluations
```

Focused mock-provider protocol-loss suite:

```bash
clankerval run --suite protocol-loss
```

Live-provider evaluation suite:

```bash
make evaluations-live
```

Focused live-provider protocol-loss proof suite:

```bash
CLNKR_EVALUATION_MODE=live-provider clankerval run --suite protocol-loss-live
```

Both make targets resolve `clankerval` from `PATH` with `python3 ./scripts/require-clankerval.py`.

Canonical CLI examples use `clankerval` directly:

```bash
clankerval run --suite default
CLNKR_EVALUATION_MODE=live-provider clankerval run --suite default
clankerval run --suite protocol-loss
CLNKR_EVALUATION_MODE=live-provider clankerval run --suite protocol-loss-live
```

`make evaluations-live` reads the first-wave runtime configuration from:

- `CLNKR_EVALUATION_MODE`
- `CLNKR_EVALUATION_API_KEY`
- `CLNKR_EVALUATION_BASE_URL`
- `CLNKR_EVALUATION_MODEL`

The focused `protocol-loss` and `protocol-loss-live` suites are invoked directly with `clankerval run --suite ...` unless you add dedicated Make targets for them. The deterministic suite uses required `outcome_diff`; the live suite uses an isolated workspace plus required `outcome_workspace_snapshot` so successful creation of a new file is measurable without relying on in-place git diff. The checked-in suites live here; execution still belongs to the external runner.

## Inspect

After a run:

1. Read `evaluations/trials/<trial-id>/bundle.json` to identify the trial and its artifact paths.
2. Inspect `evaluations/trials/<trial-id>/normalized/transcript.jsonl` for canonical transcript flow.
3. Inspect `evaluations/trials/<trial-id>/normalized/outcome.json` for the normalized end-state.
4. Inspect `evaluations/trials/<trial-id>/normalized/graders.jsonl` for required/advisory grading results.
5. Inspect `evaluations/trials/<trial-id>/raw/events.jsonl` for exact command start/done events.

The `evaluations/trials/` and `evaluations/reports/` directories are regenerated per run.
