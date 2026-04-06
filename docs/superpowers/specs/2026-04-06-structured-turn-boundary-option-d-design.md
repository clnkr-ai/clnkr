# Structured Turn Boundary Option D Design

## Goal

Restore cross-provider structured output acceptance without flattening the turn contract, and stop treating provider-local semantic validation failures as hard query errors.

## Decision

Use Option D: a shared wrapper object with a nested `anyOf` union under `turn`.

The provider wire shape becomes:

```json
{"turn":{"type":"act","command":"ls -la","question":null,"summary":null,"reasoning":null}}
```

The internal canonical shape stays:

```json
{"type":"act","command":"ls -la","reasoning":"inspect files"}
```

This split is deliberate. The wrapper exists to satisfy provider schema validators. It is not part of the agent's semantic turn model.

## Why This Split

The current core protocol, prompt, UI rendering, tests, and stored transcripts already speak in unwrapped turns. Changing the canonical shape to match the provider wrapper would spread a transport workaround across the repo.

This design keeps the accidental complexity at the edge:

- provider adapters send and receive the wrapped schema
- `turnschema` unwraps and validates provider payloads
- the rest of the system continues to speak in canonical turns

This is the information-hiding boundary we want. The provider constraint stays inside the provider ingress path.

## Current Behavior

Today both adapters send the same shared schema from `turnschema.Schema()`. That schema has a top-level `anyOf` and fails request validation on the tested Anthropic and OpenAI paths.

Today both adapters also call `turnschema.ParseProvider()` before returning a response to the agent. If local semantic validation fails there, `Query()` returns an error. `Agent.Step()` never appends the assistant message, never emits `EventProtocolFailure`, and never sends a correction message back to the model.

That means provider-local semantic failures are hard task failures today, not protocol failures.

## Target Behavior

### Request Shape

`turnschema.Schema()` returns a root object with:

- `type: "object"`
- `additionalProperties: false`
- one required property: `turn`
- `turn` contains the nested `anyOf` union for `act`, `clarify`, and `done`

This is the shared schema sent to both Anthropic and OpenAI.

### Successful Provider Response

When the provider returns a valid wrapped payload:

1. `ParseProvider()` validates the wrapper.
2. `ParseProvider()` unwraps `turn`.
3. `ParseProvider()` runs the existing strict inner-turn validation.
4. The adapter canonicalizes the inner turn.
5. The agent receives the same canonical assistant message shape it already expects.

Successful transcripts stay unchanged.

### Semantically Invalid Provider Response

When the provider returns wrapped JSON that satisfies request-level transport but fails local semantic validation:

1. The adapter preserves the raw assistant payload.
2. The adapter returns that payload in `Response.Message.Content`.
3. The adapter also returns a protocol parse error on the response object.
4. `Agent.Step()` appends the assistant message, emits `EventResponse`, emits `EventProtocolFailure`, appends `protocolCorrectionMessage(...)`, and returns `StepResult.ParseErr`.
5. `Agent.Run()` counts that failure against the existing consecutive protocol-failure budget.

This reclassifies provider-local semantic failures from hard query errors to normal protocol failures.

## Interface Changes

### `turnschema`

`turnschema` gets two responsibilities:

- build the wrapped provider schema
- validate and unwrap provider payloads back into canonical turns

`Parse()` remains the parser for canonical turn objects.

`ParseProvider()` becomes wrapper-aware. It is still shared across providers. It does not grow vendor-specific logic.

### `Response`

`Response` gets one additive field for protocol recovery:

```go
ProtocolErr error
```

This avoids changing the `Model.Query(ctx, messages) (Response, error)` signature while still letting adapters distinguish:

- request failure: returned as `error`
- protocol-invalid assistant payload: returned as `Response` plus `ProtocolErr`

### Agent Loop

`Agent.Step()` changes policy, not turn semantics.

If `resp.ProtocolErr != nil`, `Step()` uses the same correction path it already uses for `ParseTurn()` failures. There is one protocol-failure budget. No second counter.

## Frontend Consequences

`clnku` may print raw wrapped JSON when a provider payload is preserved for recovery. That is acceptable for this frontend.

The TUI already tolerates non-canonical assistant content by falling back to raw rendering when `ParseTurn()` fails. Protocol failures still surface through the existing warning event path.

## Testing Strategy

The main signal is the existing eval suite, especially `protocol-loss-live`.

Unit tests cover the narrow seams after that:

- `turnschema/turnschema_test.go`
  - wrapped schema shape
  - valid wrapped payloads
  - missing `turn`
  - non-object `turn`
  - semantically invalid inner turn after unwrap
- `anthropic/anthropic_test.go`
  - valid wrapped payload canonicalizes to the inner turn
  - invalid wrapped payload returns a response with raw content plus protocol error
- `openai/openai_test.go`
  - same cases as Anthropic
- `agent_test.go`
  - `Response.ProtocolErr` triggers protocol recovery in `Step()`
  - `Run()` retries after one wrapped semantic failure and succeeds
  - `Run()` exits after three consecutive wrapped semantic failures

## Verification

Verification runs in this order:

1. `make evaluations`
2. Live suite with `protocol-loss-live`
3. Focused Go tests for `turnschema`, `anthropic`, `openai`, and agent loop behavior

The live checks need to answer four questions:

- Do requests get accepted again on both tested providers?
- Do semantically invalid wrapped payloads become protocol retries instead of hard query failures?
- Does the single protocol-failure budget still trip after three consecutive bad turns?
- Do successful assistant messages remain canonical in transcripts and trajectories?

## Tradeoff

This design adds one translation boundary in `turnschema` and the adapter response path. That cost is worth it. The alternative is worse: make the wrapper canonical and spread a provider transport workaround into prompts, parsing, rendering, docs, tests, and persisted transcripts.

We are choosing the deeper module. `turnschema` absorbs the provider constraint so the rest of the repo does not need to care why the wrapper exists.
