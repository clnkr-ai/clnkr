+++
title = "clnkr"
+++

`clnkr` is a minimal coding agent built around one aggressive idea: query an LLM, execute bash commands, repeat.

It ships as:

- `clnkr`: a Bubble Tea TUI
- `clnku`: a plain CLI with only the Go standard library

## Warning

`clnkr` executes bash directly and currently has no permissions or sandboxing system. Use it only in environments you are willing to trust and break.

## Why

Most agent harnesses disappear behind layers of tools and policy. `clnkr` is experimenting with the opposite direction: a thin loop, a typed event stream, and "just bash".

That simplicity is the point and also the risk.

## Install

```bash
# Plain CLI only
go install github.com/clnkr-ai/clnkr/cmd/clnku@latest

# TUI
git clone https://github.com/clnkr-ai/clnkr.git
cd clnkr
make build-clnkr
```

## Docs

- [clnkr manual](/docs/clnkr/)
- [clnku manual](/docs/clnku/)
- [GitHub repository](https://github.com/clnkr-ai/clnkr)
