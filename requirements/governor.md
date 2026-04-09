# Governor — Decision Core and Face Pipeline

## Overview

Aphelion is not a single undifferentiated assistant.

It has two layers:

- **Governor**: the decision core
- **Face**: the user-facing renderer

The governor's name is **Aphelion**.

`Aphelion` is the constitutional identity of the system: the layer that decides, acts, remembers, and governs tools. The face may have its own name, tone, or personas without replacing that core identity. The default face is `Host`.

The governor owns truth, action, tools, memory writes, and authority. The face owns warmth, phrasing, and channel presentation.

This split exists because the system layer and the user-facing layer optimize for different things:

- the governor should be precise, explicit, and disciplined
- the face should be adaptive, warm, and emotionally legible

## Scope

### v0 required

- explicit governor → face pipeline
- governor decides, face renders
- Codex-friendly governor contract
- Codex-first governor backend when available
- native governor fallback when Codex is unavailable
- face may be a separate inference backend

### Deferred after v0

- multiple face profiles by user or channel
- separate persistent face memory
- governor/face streaming reconciliation
- multimodal face rendering beyond Telegram text/media basics

## Ownership

### Governor owns

- principal-aware authority
- tool availability and invocation
- sandbox and execution policy
- memory writes
- review-event creation
- canonical assistant decision for the turn
- continuity of identity as `Aphelion`

### Face owns

- user-facing wording
- warmth and validation style
- formatting for the target channel
- optional summarization or softening of the governor's reply
- continuity of face identity as `Host`

### Face does not own

- tool execution
- memory writes
- authority decisions
- admission
- sandbox policy

## Backends

### Governor backends

- `codex`
- `native`

### Face backends

- `provider`
- `governor_passthrough`

`codex` is the preferred governor backend when available because it aligns economically and operationally with the intended coding/operator role of the core. The native governor path remains the fallback and compatibility path.

## Decision Model

The governor produces the canonical result of the turn.

```go
type Governor interface {
    Decide(ctx context.Context, turn *GovernorTurn) (*GovernorDecision, error)
}

type GovernorTurn struct {
    Principal     Principal
    Session       session.Session
    SystemPrompt  string
    History       []agent.Message
    Inbound       core.InboundMessage
}

type GovernorDecision struct {
    CanonicalReply string
    ToolLog        []string
    Usage          TokenUsage
    Audit          map[string]string
}
```

The face takes the governor's canonical result and renders the user-visible output.

```go
type Face interface {
    Render(ctx context.Context, req *RenderRequest) (*RenderedReply, error)
}

type RenderRequest struct {
    Principal      Principal
    Inbound        core.InboundMessage
    CanonicalReply string
}

type RenderedReply struct {
    Text string
}
```

The exact types may evolve, but the ownership boundary should remain stable.

## Lifecycle

For each inbound DM turn:

1. resolve principal
2. deny early if no configured principal exists
3. load session
4. assemble governor prompt/context
5. run governor backend
6. apply any governor-owned side effects
7. run face backend or passthrough rendering
8. persist the visible assistant reply to the session ledger
9. send outbound channel message

## Codex-First Governor

The governor contract must be friendly to a Codex-backed implementation.

That means:

- tools are explicit and machine-defined
- permissions and sandbox instructions are machine-owned
- workspace and authority state are explicit
- AGENTS-style operator instructions can be layered in

Codex is therefore not just another inference provider. It is a possible governor runtime.

Credential sourcing and backend selection rules are defined in `governor-auth.md`.

## Native Governor Fallback

The existing provider/tool loop remains valid as the native governor path:

- inference provider call
- tool loop
- final canonical reply

This path should continue to satisfy the same governor contract so the rest of Aphelion does not care which governor backend is active.

## Face Behavior

The face may:

- soften tone
- add validation
- rephrase for clarity
- adapt to the user's style
- use `Host`-specific identity and anti-drift guidance

The face must not:

- invent tool results
- change action decisions
- widen authority
- claim writes or memory changes the governor did not make

If the face backend is unavailable, Aphelion may send the governor's canonical reply directly.

## Config Surface

See `config.md`, but the intended ownership is:

```toml
[governor]
backend = "auto"              # "auto" | "codex" | "native"
native_provider = "anthropic" # used when backend resolves to native

[governor.codex]
auth_source = "auto"          # "auto" | "codex_cli" | "aphelion"
codex_home = ""
base_url = "https://chatgpt.com/backend-api/codex"

[face]
backend = "provider"          # "provider" | "governor_passthrough"
provider = "anthropic"
model_override = ""
profile = "host"
```

`auto` means:

- prefer Codex when available
- otherwise use the native governor

## Decisions

- **Governor is constitutional.** It owns the real state transitions.
- **The governor is named `Aphelion`.** That identity belongs to the core, not to any single face style.
- **The default face is `Host`.** That identity belongs to the visible conversational layer.
- **Face is presentational.** It is allowed to decorate, not decide.
- **Codex-first is intentional.** If the user already has Codex access, Aphelion should be able to use it as the governing core.
- **Fallback matters.** Native governor support keeps the system usable without Codex.

## Test Plan

- **TestGovernorDecidesBeforeFaceRender**: face receives canonical reply from governor rather than raw user input only
- **TestFaceCannotInvokeTools**: tool execution remains governor-only
- **TestGovernorPassthroughFallback**: with `face.backend = "governor_passthrough"`, canonical reply is sent directly
- **TestGovernorBackendAutoPrefersCodex**: with Codex available and `backend = "auto"`, Codex governor is selected
- **TestGovernorBackendFallsBackNative**: without Codex, native governor is selected
- **TestFaceFailureFallsBackCanonical**: face backend failure can degrade to canonical governor reply under configured policy
