---
name: boundary-contract
description: Use before implementing a structural change in clnkr to prevent file-splitting without real ownership or cohesion.
---

# Boundary Contract

Use this before adding packages, moving code, extracting files, or accepting an
agent-authored design that changes clnkr's architecture.

## Required Contract

Write the contract before implementation:

- Owner: package, file, or layer that owns the behavior.
- Inbound callers: which packages/files may call into it.
- Outbound dependencies: which packages/files it may depend on.
- State and data owned: what it stores, computes, or validates.
- Boundary exclusions: what must not cross this boundary.
- Depth test: why this is a deeper interface, not a one-for-one file split.
- Verification: command or report that will show the boundary held.

## Review Rules

Reject the design if:

- ownership is named by filename only
- every extracted function is still called by the old owner
- both sides import or call back into each other
- the new module is mostly pass-through wrappers
- the justification is "organization" without lower coupling or clearer ownership

Prefer existing clnkr boundaries:

- root package: importable core agent API and typed events
- `internal/core/`: shared core helpers that do not depend on frontend or providers
- `internal/providers/`: provider adapters and provider wire protocols
- `internal/providerfactory/`: frontend-safe provider construction boundary
- `cmd/internal/clnkrapp/`: CLI coordination and session/provider composition
- `cmd/clnkr/` and `cmd/clnkrd/`: executable adapters

Use `make architecture-shape-report` and `make semantic-cohesion-report` as
manual evidence when a structural claim depends on coupling or cohesion.
