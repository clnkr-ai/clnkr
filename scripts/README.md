# scripts

Small repo-maintenance helpers live here.

Rules:
- Group by purpose: `check-*`, `install-*`, `sync-*`, `release-*`.
- Put human entrypoints in `make` only when people should run them directly.
- Let CI call helpers in `scripts/` directly when they are workflow-only.
- Keep each helper narrow. Split it when it starts doing unrelated work.
