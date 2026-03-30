# Eval Harness

This directory holds end-to-end evals for `clnku`.

The harness shells out to the real `clnku` binary, points it at a local OpenAI-compatible fixture server, and checks the resulting run against semantic expectations plus the expected final workspace.

## Layout

Each case lives under `testdata/cases/<case>/`.

Inputs:

- `manifest.json`: task, cwd, step limit, optional seed transcript, and the checks to run
- `input/task.txt`: prompt passed to `clnku -p`
- `input/workspace/`: starting workspace copied into a temp root
- `input/home/`: optional files copied into `HOME`
- `input/config/`: optional files copied into `XDG_CONFIG_HOME`
- `input/project/AGENTS.md`: project instructions copied into the temp workspace
- `input/model-turns.json`: ordered assistant responses returned by the fixture server
- `input/seed-messages.json`: optional transcript seed for `--load-messages`

Expected outputs:

- `expected/workspace/`

Runtime artifacts go under `artifacts/<case>/`:

- `trajectory.json`
- `event-log.jsonl`
- `workspace/`

That directory is ignored by git and exists to keep the captured run traces and final workspace inspectable without rerunning the case.

## Cases

- `001-basic-edit`: one command writes a file

## Run

```bash
make eval
```

`go test ./...` also runs these evals because `eval/` is part of the root module.

Modes:

- Default: fixture mode. `make eval` uses the local scripted OpenAI-compatible server from each case's `input/model-turns.json`.
- Live: `CLNKR_EVAL_MODE=live make eval` runs the same cases against a real OpenAI-compatible endpoint.

Live mode defaults:

- `CLNKR_EVAL_API_KEY=${CLNKR_EVAL_API_KEY:-$OPENAI_API_KEY}`
- `CLNKR_EVAL_BASE_URL=${CLNKR_EVAL_BASE_URL:-$OPENAI_BASE_URL}`
- `CLNKR_EVAL_MODEL=${CLNKR_EVAL_MODEL:-gpt-5.4-nano}`

Examples:

```bash
CLNKR_EVAL_MODE=live make eval
CLNKR_EVAL_MODE=live CLNKR_EVAL_API_KEY=foo-key CLNKR_EVAL_BASE_URL=https://myllm/ CLNKR_EVAL_MODEL=bar-model make eval
```

## CI

- `CI` runs fixture evals in a separate `eval-fixture` job via `make eval`.
- `Live Evals` is a separate workflow. It runs on `workflow_dispatch` and pushes to `main`.
- `Live Evals` uses `OPENAI_API_KEY` from GitHub Actions secrets.
- `Live Evals` optionally reads `CLNKR_EVAL_BASE_URL` and `CLNKR_EVAL_MODEL` from repository variables.
- If `OPENAI_API_KEY` is unset, the live workflow skips the eval job.

## Update expectations

1. Run `make eval`.
2. If command text or exit behavior changed, update `manifest.json`:
   - `expect.commands`
   - `expect.exit_codes`
   - exact command text is enforced in fixture mode
   - live mode still checks command count and exit codes, but not byte-for-byte command equality
3. If the final workspace changed intentionally, update `eval/testdata/cases/<case>/expected/workspace/`.
4. Run `make eval` again and confirm the case is green.

The harness normalizes temp-root paths to placeholders like `<WORKDIR>` so the checked-in expectations stay stable across runs.
