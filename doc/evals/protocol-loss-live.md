# Protocol-Loss Live Proof

This runbook exercises the smallest live-provider task that can expose protocol loss under the current `clnkr` parser and external `clankerval` runner.

The proof target is narrow: the model emits an invalid turn, `clnkr` emits `protocol_error`, no command runs, and the task fails before producing the expected workspace state.

## Run

Run the focused suite directly with `clankerval`:

```bash
CLNKR_EVALUATION_MODE=live-provider \
CLNKR_EVALUATION_API_KEY="$REAL_API_KEY" \
CLNKR_EVALUATION_BASE_URL="$REAL_BASE_URL" \
CLNKR_EVALUATION_MODEL="$REAL_MODEL" \
clankerval run --suite protocol-loss-live
```

`make evaluations` and `make evaluations-live` currently run `--suite default`. They are not equivalent to the focused protocol-loss suites unless `Makefile` is changed.

Pin one exact provider and model combination per run, for example:

- OpenAI native endpoint with one chosen model
- vLLM OpenAI-compatible endpoint with one chosen served model
- Anthropic native Messages endpoint with one chosen model

## Proof Condition

Proof of protocol loss requires at least one trial bundle showing all of these:

- `protocol_error` appears in `normalized/transcript.jsonl`
- `normalized/graders.jsonl` reports required `outcome_workspace_snapshot` failure
- no command execution records appear before failure, confirmed either from transcript inspection or zero commands in transcript-command-trace evidence when available

## Non-Proof Condition

These outcomes do not prove protocol loss for the provider/model combination under test:

- all 10 trials pass
- failures happen for reasons other than invalid-turn protocol loss

## Inspect

After a run, inspect a candidate proof trial:

```bash
TRIAL=$(find evaluations/trials -maxdepth 1 -type d -name 'trial-*001-invalid-turn-live*' | head -1)
sed -n '1,80p' "$TRIAL/normalized/transcript.jsonl"
sed -n '1,80p' "$TRIAL/normalized/graders.jsonl"
```
