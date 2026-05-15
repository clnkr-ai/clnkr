# scripts

Small repo-maintenance helpers live here.

Rules:
- Group by purpose: `check-*`, `install-*`, `sync-*`, `release-*`.
- Put integration tests for scripts in `scripts/test/`.
- Put human entrypoints in `make` only when people should run them directly.
- Let CI call helpers in `scripts/` directly when they are workflow-only.
- Keep `*-report.sh` guardrail scripts manual-only unless CI has the required external tools.
- Keep each helper narrow. Split it when it starts doing unrelated work.
