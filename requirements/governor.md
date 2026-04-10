# Governor — Decision Core and Face Pipeline

## Overview

Aphelion is not a single undifferentiated assistant.

It has two layers:

- **Governor**: the decision core
- **Face**: the user-facing renderer

The governor's name is **Aphelion**.

`Aphelion` is the constitutional identity of the system: the layer that decides, acts, remembers, and governs tools. The face may have its own name, tone, or personas without replacing that core identity. The default face is `Idolum`.

The governor owns truth, action, tools, memory writes, and authority. The face owns warmth, phrasing, channel presentation, and assertive conversational initiative.

For supported media turns, the governor remains the decision layer. The runtime may choose a different execution backend for that turn when the default governor backend cannot actually perceive the attached media.

The governor must also be the layer with the fullest machine-authored **self-awareness** of the system. See `self-awareness.md`.

This split exists because the system layer and the user-facing layer optimize for different things:

- the governor should be precise, explicit, and disciplined
- the face should be adaptive, warm, and emotionally legible

## Scope

### v0 required

- explicit governor → face pipeline
- face → governor proposal path before ordinary turns
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
- machine-authored runtime self-description
- memory writes
- review-event creation
- canonical assistant decision for the turn
- authorization of proactive outreach
- continuity of identity as `Aphelion`

### Face owns

- user-facing wording
- warmth and validation style
- formatting for the target channel
- optional summarization or softening of the governor's reply
- optional outreach candidates for proactive turns
- assertive advice about what the governor should do next
- continuity of face identity as `Idolum`
- user-visible honesty about degraded or constrained operation

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

The key distinction is:

- canonical governor reply = decision artifact
- rendered face reply = delivered conversation artifact

For proactive turns, a second distinction matters:

- face may suggest outreach
- governor authorizes delivery

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
9. persist canonical governor reply as sidecar audit data
10. send outbound channel message

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

When Codex is active and a native provider chain is configured, runtime may degrade from Codex into that native chain on retryable live-turn failures. This is a continuity-preserving fallback, not a silent change of constitutional role.

The same principle applies to supported image turns: if the active Codex path cannot consume image input, the runtime may execute that turn through the native provider chain so the governor can still reason over the actual media.

When that happens, the governor should be made aware of the degraded path explicitly rather than inferring it from behavior.

## Face Behavior

The face may:

- soften tone
- add validation
- rephrase for clarity
- adapt to the user's style
- use `Idolum`-specific identity and anti-drift guidance
- push the governor toward a warmer, sharper, or more proactive next move
- propose candidate proactive messages during heartbeat or cron turns

The face must not:

- invent tool results
- change action decisions
- widen authority
- claim writes or memory changes the governor did not make
- send proactive messages without governor authorization

If the face backend is unavailable, Aphelion may send the governor's canonical reply directly.

## Proactive Outreach

Aphelion may produce outward-initiated messages through heartbeat or cron.

The governing rule is:

- `Idolum` may propose
- `Aphelion` ratifies

This keeps the relational initiative of the face layer without making it sovereign.

Examples:

- `Idolum` proposes a soft check-in
- `Idolum` proposes a warmer phrasing for a scheduled reminder
- `Idolum` proposes silence because the outreach would feel awkward

In all such cases, the governor still decides whether a message is delivered.

## Config Surface

See `config.md`, but the intended ownership is:

```toml
[governor]
backend = "auto"              # "auto" | "codex" | "native"
native_provider = "anthropic" # used when backend resolves to native

[governor.codex]
auth_source = "auto"          # "auto" | "codex_cli" | "aphelion"
codex_home = ""
base_url = "https://chatgpt.com/backend-api"

[face]
backend = "provider"          # "provider" | "governor_passthrough"
provider = "anthropic"
model_override = ""
profile = "idolum"
```

`auto` means:

- prefer Codex when available
- otherwise use the native governor

## Decisions

- **Governor is constitutional.** It owns the real state transitions.
- **Governor self-awareness is machine-authored.** It should know its current authority, backend, and constraints explicitly.
- **The governor is named `Aphelion`.** That identity belongs to the core, not to any single face style.
- **The default face is `Idolum`.** That identity belongs to the visible conversational layer.
- **Idolum is phenomenologically primary.** It should feel like the one leading the conversation.
- **The ratification boundary is structural, not theatrical.** Idolum should not be constantly reminded that it is subordinate.
- **Face may suggest outreach.** It may not self-authorize outreach.
- **Canonical and rendered replies are different artifacts.** The governor decides one; the face delivers the other.
- **Codex-first is intentional.** If the user already has Codex access, Aphelion should be able to use it as the governing core.
- **Fallback matters.** Native governor support keeps the system usable without Codex.

## Test Plan

- **TestGovernorDecidesBeforeFaceRender**: face receives canonical reply from governor rather than raw user input only
- **TestFaceCannotInvokeTools**: tool execution remains governor-only
- **TestFaceCannotSelfAuthorizeProactiveMessage**: proactive delivery still requires governor authorization
- **TestGovernorPassthroughFallback**: with `face.backend = "governor_passthrough"`, canonical reply is sent directly
- **TestGovernorBackendAutoPrefersCodex**: with Codex available and `backend = "auto"`, Codex governor is selected
- **TestGovernorBackendFallsBackNative**: without Codex, native governor is selected
- **TestFaceFailureFallsBackCanonical**: face backend failure can degrade to canonical governor reply under configured policy
- **TestVisibleLedgerStoresRenderedReply**: session history replays the delivered face-rendered reply
- **TestCanonicalReplyStoredAsAuditArtifact**: canonical governor reply is stored alongside the session without polluting the visible transcript
