# Security policy

## Reporting a vulnerability

If you find a security issue in hew, email cosgroveb@gmail.com. Do not open a public issue.

You should hear back within 72 hours. If confirmed, a fix will be released as soon as practical and you will be credited in the release notes (unless you prefer otherwise).

## Scope

hew executes bash commands suggested by an LLM. By design, it runs arbitrary code. The security boundary is between hew and the systems it talks to:

- **API key handling** — keys should not leak into logs, event logs, or trajectories
- **File path arguments** — `--event-log`, `--trajectory`, and `--load-messages` should not allow path traversal or unsafe file operations beyond what the user explicitly requested
- **Untrusted input** — malformed JSON in `--load-messages` or crafted LLM responses should not cause crashes or unexpected behavior beyond normal error exits
