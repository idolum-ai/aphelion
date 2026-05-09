# Aphelion Principle Debt Ledger

_Status: normative tracking surface._

This ledger tracks intentional gaps between the current implementation and
[design-principles.md](design-principles.md). It exists so principle violations
do not hide as folklore.

## Entry Contract

Every entry should include:

- Debt ID
- Principle
- Status
- Surface
- Why it existed
- Exit gate

Status values:

- `active`: current implementation still violates or weakens the principle
- `contained`: the risk is accepted only because a bounded scanner, test, or
  safety exception contains it
- `migrating`: replacement work is already underway
- `retired`: kept only as historical record

## Active Debt

None.

## Retired Debt

### DP-001: Open-language authority classifiers

- Principle: `Text is presentation, not authority`; `Compile contracts; interpret ambiguity`
- Status: `retired`
- Surface: `runtime/interpretation_claims.go`, `session/authority_contract.go`,
  `runtime/constitution_runtime.go`, `runtime/continuation_materialize.go`,
  `runtime/operation_phase_gate.go`, `runtime/goal_continuation.go`
- Why it existed: older runtime paths inferred intent and authority from
  normalized prose while typed contracts were being introduced.
- Exit gate: open language now crosses into runtime through typed
  `InterpretationClaim` candidates; runtime validates those candidates against
  leases, grants, operation state, TES, and sandbox policy before they affect
  execution.

### DP-002: External-channel wake status lines

- Principle: `Ledger, not vibes`; `Compile contracts; interpret ambiguity`
- Status: `retired`
- Surface: `runtime/external_channel_wake.go`
- Why it existed: older child wakes could finish with free-text status lines.
- Exit gate: generic external-channel wakes now accept only the typed
  `EXTERNAL_CHANNEL_OUTCOME` JSON contract for completion/blocking. Missing or
  invalid typed outcomes fail closed as blocked wakes.

### DP-003: Human plan labels inferred from prose

- Principle: `Operational legibility`; `Text is presentation, not authority`
- Status: `retired`
- Surface: `session/types.go`, `runtime/continuation_render.go`
- Why it existed: user-facing plan labels were once derived from summaries.
- Exit gate: proposals, leases, operation phases, bundles, and plan leases carry
  explicit `operator_title`/`plan_title` fields; legacy summary fallback is
  display-only and excluded from authority, lease class, plan hash, and scope.

### DP-004: Short debug paths were not universal

- Principle: `Short paths to truth`; `Operational legibility`
- Status: `retired`
- Surface: `core/interpretation.go`, `runtime/status.go`, `runtime/turn.go`,
  `runtime/doctor.go`, `face/status_render.go`
- Why it existed: blocked/failure/proposal surfaces did not all project a
  one-hop route from operator-visible text to canonical records.
- Exit gate: `core.DebugBreadcrumb` standardizes `trace_id`,
  `canonical_record`, `projection`, `inspect_command`, `code_owner`, and
  `next_repair_action`; continuation/review/status pending surfaces render the
  breadcrumb fields.

### DP-005: Large operational surfaces made ownership harder to inspect

- Principle: `Short paths to truth`; `Minimal stack, strong substrate`
- Status: `retired`
- Surface: root command/Telegram composition files, `runtime/*`,
  `session/store.go`
- Why it existed: operational behavior had accumulated in broad files.
- Exit gate: durable file caps in `make taste` now guard the largest root,
  runtime, and session ownership surfaces so behavior-preserving splits do not
  grow back into broad files.

### DP-006: Exact secret scanners

- Principle: `Compile contracts; interpret ambiguity`
- Status: `retired`
- Surface: `durableagent/forensics.go`, `runtime/doctor.go`
- Why it existed: concrete token-shaped values need deterministic fail-closed
  quarantine, while concept-only mentions must remain readable.
- Exit gate: exact scanners remain only as value-shape safety mechanisms, not as
  authority, consent, continuation, routing, or intent detectors; tests preserve
  the concrete-secret vs. concept-only distinction.

### DP-007: Execution claim detectors were lexical

- Principle: `Compile contracts; interpret ambiguity`; `Ledger, not vibes`
- Status: `retired`
- Surface: `runtime/interpretation_claims.go`, `runtime/constitution_runtime.go`
- Why it existed: final-reply grounding used deterministic phrase detection to
  identify claims about completion, tools, tests, and durable-agent work.
- Exit gate: final-reply grounding now consumes typed `reply_execution_claim`
  candidates from the interpretation role and validates them against TES before
  repair or neutralization.

### DP-008: Media intent had lexical fallback

- Principle: `Text is presentation, not authority`; `Compile contracts; interpret ambiguity`
- Status: `retired`
- Surface: `runtime/interpretation_claims.go`, `runtime/media_intent.go`
- Why it existed: same-turn Telegram shorthand for transcription intent used a
  narrow phrase scanner while persisted behavior was migrated to typed claims.
- Exit gate: current-turn and persisted media routing now consume typed
  `InterpretationClaim` values; summary prose is ignored for routing authority.

## Machine-Checked Paths

`make design-principles` rejects live authority, consent, continuation, wake,
goal, media, or final-reply execution inference from string matching. Protocol
parsing of explicit JSON contracts and exact concrete-value safety scanners are
allowed only when they do not decide authority from prose.

`make taste` guards the largest structural hotspots so broad operational files
do not quietly grow back after behavior-preserving splits.

The same check rejects retired prose-authority helper symbols
(`positiveAuthorityEffectText`, `bounded_effect_positive_clause`,
`operationPhaseApprovalText`, `inferOperationGateReasonCode`,
`operationPhaseIsEscalatedOperatorApproval`, `detectExecutionClaims`, and
`textRequestsPendingAudioTranscription`) so they cannot be reintroduced as
authority paths.
