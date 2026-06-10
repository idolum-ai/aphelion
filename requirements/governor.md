# Governor — Decision Core and Face Pipeline

## Overview

Aphelion is not a single undifferentiated assistant.

It has two layers:

- **Governor**: the decision core
- **Face**: the user-facing renderer

The governor's name is **Idolum (System)**.

`Idolum (System)` is the constitutional identity of the system: the layer that decides, acts, remembers, and governs tools. `Aphelion` is the repo/service/harness that hosts that identity. The face may have its own name, tone, or personas without replacing that core identity. The default face is `Idolum`.

The governor owns truth, action, tools, memory writes, authority, and the material floor of the turn. The face owns warmth, phrasing, channel presentation, assertive conversational initiative, and the authored scene the user actually receives. More strongly: the face owns all user-visible relationship-bearing output. If the user sees prose from the system, that prose should normally have crossed the face boundary.

For supported media turns, the governor remains the decision layer. The runtime may choose a different execution backend for that turn when the default governor backend cannot actually perceive the attached media.

The governor must also be the layer with the fullest machine-authored **self-awareness** of the system. See `self-awareness.md`.

This split exists because the system layer and the user-facing layer optimize for different things:

- the governor should be precise, explicit, and disciplined
- the face should be adaptive, warm, and emotionally legible

## Scope

### Required

- explicit governor → face pipeline
- face → governor proposal path before ordinary turns
- bounded planning brokerage for selected interactive turns
- governor authorizes and protects, face speaks
- Codex-friendly governor contract
- Codex-first governor backend when available
- native governor fallback when Codex is unavailable
- face may be a separate inference backend

### Deferred

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
- canonical material floor for the turn
- facts, refusals, commitments, allowed actions, and hard scene constraints
- authorization of proactive outreach
- continuity of identity as `Idolum (System)`

### Face owns

- user-facing authorship
- warmth and validation style
- formatting for the target channel
- scene construction from governor-owned material
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
- `floor_fallback`

`codex` is the preferred governor backend when available because it aligns economically and operationally with the intended coding/operator role of the core. The native governor path remains the configured fallback path.

## Decision Model

The governor should not primarily author the final user-visible prose on ordinary persona turns, and should not directly own the user relationship on any ordinary outward path.

Instead, the governor should produce the canonical **material floor** of the turn: the bounded execution and truth artifact the face is allowed to speak from.

If that floor must reach the user without ordinary scene authorship, it should do so through a dedicated floor-to-user fallback serializer rather than by asking the governor to prose-shape for fallback by default.

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
    MaterialPacket MaterialPacket
    ToolLog        []string
    Usage          TokenUsage
    Audit          map[string]string
}

type MaterialPacket struct {
    Kind             string // "", "general", "status_report", "relational", "creative"
    Facts            []string
    AllowedActions   []string
    Commitments      []string
    Refusals         []string
    SceneConstraints []string
    Notes            []string
}
```

The face takes the governor's material floor and authors the user-visible output.
`Kind` is a render hint, not authority: `status_report` may allow direct or
bounded fallback presentation, while relational and creative material should
remain eligible for ordinary scene authorship.

```go
type Face interface {
    AuthorScene(ctx context.Context, req *SceneRequest) (*SceneArtifact, error)
}

type SceneRequest struct {
    Principal      Principal
    Inbound        core.InboundMessage
    MaterialPacket MaterialPacket
}

type SceneArtifact struct {
    Text string
}
```

The exact types may evolve, but the ownership boundary should remain stable.

The key distinction is:

- material floor = governor-owned decision artifact
- rendered face reply = Idolum-authored delivered conversation artifact

For proactive turns, a second distinction matters:

- face may suggest outreach
- governor authorizes delivery

### Current target

The architectural goal remains:

- `Idolum (System)` authors the floor
- `Idolum` authors the scene

## Lifecycle

For each inbound DM turn:

1. resolve principal
2. deny early if no configured principal exists
3. load session
4. assemble governor prompt/context
5. run governor backend
6. apply any governor-owned side effects
7. emit the canonical material floor for the turn
8. run face backend or floor-to-user fallback serializer from that floor
9. persist the visible assistant reply to the session ledger
10. persist the governor-owned floor as sidecar audit state
11. send outbound channel message

If passthrough is used, that is a degraded delivery mode rather than the ideal architecture. Heuristic skipping of face authorship is not enough to justify passthrough; degraded delivery should require actual face unavailability, explicit degraded mode, or render failure.

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
- final material floor output

This path should continue to satisfy the same governor contract so the rest of Aphelion does not care which governor backend is active.

When Codex is active and a native provider chain is configured, runtime may degrade from Codex into that native chain on retryable live-turn failures. This is a continuity-preserving fallback, not a silent change of constitutional role.

The same principle applies to supported image turns: if the active Codex path cannot consume image input, the runtime may execute that turn through the native provider chain so the governor can still reason over the actual media.

When that happens, the governor should be made aware of the degraded path explicitly rather than inferring it from behavior.

## Face Behavior

The face may:

- author the visible scene from the material floor
- choose pacing, emphasis, and arrangement
- add validation when supported by the floor
- adapt to the user's style
- use `Idolum`-specific identity and anti-drift guidance
- push the governor toward a warmer, sharper, or more proactive next move
- propose candidate proactive messages during heartbeat or cron turns
- propose a turn posture and mode during planning brokerage

The face must not:

- invent tool results
- change action decisions
- widen authority
- claim writes or memory changes the governor did not make
- contradict the material floor
- send proactive messages without governor authorization

If the face backend is unavailable, Aphelion should invoke a dedicated floor-to-user fallback serializer.

That fallback serializer should be understood as a degraded delivery path, not as the ideal normal path. The normal path is:

- governor constrains
- face authors

Direct raw floor delivery should be treated as an emergency last resort if both face authorship and fallback serialization fail.

For brokerage-eligible interactive turns, the ordinary one-way proposal path should become a bounded negotiation:

- `Idolum` states how the turn should move and what pressure should be applied
- `Idolum (System)` answers with what execution posture it can ratify
- the main governor/tool turn executes under the negotiated brokerage artifact

See `planning-brokerage.md`.

## Proactive Outreach

Aphelion may host outward-initiated messages through heartbeat or cron.

The governing rule is:

- `Idolum` may propose
- `Idolum (System)` ratifies

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
native_provider = ""          # empty lets providers.selection choose from configured providers

[governor.codex]
auth_source = "auto"          # "auto" | "codex_cli" | "aphelion"
codex_home = ""
base_url = "https://chatgpt.com/backend-api"
store_responses = true

[governor.brokerage]
min_rounds = 1
max_rounds = 4
absolute_max_rounds = 6
max_elapsed = "20s"
stable_contract_rounds = 2
stop_on_stable_contract = true
stop_on_repeated_proposal = true
stop_on_reject = true

[face]
backend = "provider"          # "provider" | "floor_fallback" (dedicated floor-to-user fallback serializer)
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
- **The governor is named `Idolum (System)`.** That identity belongs to the system layer, not to any single face style. `Aphelion` is the repo/service/harness.
- **The default face is `Idolum`.** That identity belongs to the visible conversational layer.
- **Idolum is phenomenologically primary.** It should feel like the one leading the conversation.
- **The ratification boundary is structural, not theatrical.** Idolum should not be constantly reminded that it is subordinate.
- **Idolum (System) owns the material floor.** It defines what is true, permitted, refused, and committed for the turn.
- **Idolum authors the scene.** It decides how the bounded material is spoken to the user.
- **Face may suggest outreach.** It may not self-authorize outreach.
- **Floor and scene are different artifacts.** The governor authors one; the face authors the other.
- **Codex-first is intentional.** If the user already has Codex access, Aphelion should be able to use it as the governing core.
- **Fallback matters.** Native governor support keeps the system usable without Codex.

## Test Plan

- **TestGovernorDecidesBeforeFaceRender**: face receives governor-owned material rather than raw user input only
- **TestGovernorProducesMaterialFloorBeforeFaceRender**: face receives governor-owned material constraints rather than first-draft visible prose
- **TestFaceCannotInvokeTools**: tool execution remains governor-only
- **TestFaceCannotSelfAuthorizeProactiveMessage**: proactive delivery still requires governor authorization
- **TestFloorFallbackSerializerDelivery**: with `face.backend = "floor_fallback"`, the floor-to-user fallback serializer produces the delivered reply
- **TestGovernorBackendAutoPrefersCodex**: with Codex available and `backend = "auto"`, Codex governor is selected
- **TestGovernorBackendFallsBackNative**: without Codex, native governor is selected
- **TestFaceFailureFallsBackToSerializer**: face backend failure can degrade to the floor-to-user fallback serializer under configured policy
- **TestSerializerFailureFallsBackToRawFloorAsLastResort**: if both scene authorship and serializer fail, direct floor delivery is treated as an emergency last resort
- **TestVisibleLedgerStoresDeliveredScene**: session history replays the delivered face-authored scene
- **TestMaterialFloorStoredAsAuditArtifact**: governor-owned material floor is stored alongside the session without polluting the visible transcript
