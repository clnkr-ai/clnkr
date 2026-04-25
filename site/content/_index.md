+++
title = "clnkr"
+++

`clnkr` is a minimal coding agent built around one aggressive idea: query an LLM, execute bash commands, repeat.

It ships as `clnku`, a plain CLI with only the Go standard library.

## Warning

`clnkr` executes bash directly and currently has no permissions or sandboxing system. Use it only in environments you are willing to trust and break.

## Why

Most agent harnesses disappear behind layers of tools and policy. `clnkr` is experimenting with the opposite direction: a thin loop, a typed event stream, and "just bash".

That simplicity is the point and also the risk.

## Install

```bash
go install github.com/clnkr-ai/clnkr/cmd/clnku@latest
```

## Docs

- [evaluations](/evaluations/)
- [clnku manual](/docs/clnku/)
- [clankerval manual](/docs/clankerval/)
- [GitHub repository](https://github.com/clnkr-ai/clnkr)
