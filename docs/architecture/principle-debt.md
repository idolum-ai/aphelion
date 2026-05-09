# Aphelion Principle Debt Ledger

_Status: normative tracking surface._

This ledger tracks known, intentional gaps between the current implementation and
[design-principles.md](design-principles.md). It exists so principle violations
do not hide as folklore.

Debt here is not permission to keep drifting. It is a named migration target with
an owner surface, a reason it still exists, and an exit gate.

## Entry Contract

Every entry should include:

- Debt ID
- Principle
- Status
- Surface
- Why it exists
- Exit gate

Status values:

- `active`: current implementation still violates or weakens the principle
- `contained`: the risk is accepted only because a bounded scanner, test, or
  safety exception contains it
- `migrating`: replacement work is already underway
- `retired`: kept only as historical record

## Active Debt

### DP-001: Open-language authority classifiers

- Principle: `Text is presentation, not authority`; `Compile contracts; interpret ambiguity`
- Status: `migrating`
- Surface: `session/authority_contract.go`, `runtime/constitution_runtime.go`,
  `runtime/continuation_materialize.go`, `runtime/operation_phase_gate.go`,
  `runtime/goal_continuation.go`
- Why it exists: Continuation authority no longer reads `bounded_effect` prose,
  and operation-phase gates now compile from typed fields plus exact structured
  enum compatibility. Older constitution, goal-continuation, and a few
  compatibility surfaces still infer claims from normalized text.
- Exit gate: Add an interpretation-to-contract lane that returns typed claims
  such as `intent`, `authority_class`, `consent_subject`, `risk`,
  `missing_context`, and `confidence`; runtime must validate those claims
  against leases, grants, operation state, and TES. Retire the remaining
  normalized-text compatibility once those typed claims cover legacy turns.

### DP-002: External-channel wake status lines

- Principle: `Ledger, not vibes`; `Compile contracts; interpret ambiguity`
- Status: `migrating`
- Surface: `runtime/external_channel_wake.go`
- Why it exists: Child wake completion now prefers a typed
  `EXTERNAL_CHANNEL_OUTCOME` JSON contract with schema, enum status, reason
  code, adapter, child ID, grant ID, and evidence refs. Legacy
  `EXTERNAL_CHANNEL_STATUS` lines remain as a compatibility fallback for older
  child replies.
- Exit gate: Durable child wakes must emit a typed wake-outcome artifact with
  enum status, reason code, grant ID, adapter, child ID, and evidence refs. Text
  can summarize that artifact but must not be the completion contract. Remove
  legacy status-line fallback after all live children have emitted typed
  outcomes through at least one maintenance cycle.

### DP-003: Human plan labels inferred from prose

- Principle: `Operational legibility`; `Text is presentation, not authority`
- Status: `active`
- Surface: `runtime/continuation.go`
- Why it exists: Operator-facing labels improved, but some fallback labels still
  infer names and plan titles from summaries.
- Exit gate: `ActionProposal`, `ContinuationLease`, and operation phases should
  carry explicit `operator_title` or `plan_title` fields. Label fallback should
  become display-only and never influence authority.

### DP-004: Short debug paths are not yet universal

- Principle: `Short paths to truth`; `Operational legibility`
- Status: `active`
- Surface: `runtime/status.go`, `runtime/turn.go`, `runtime/doctor.go`,
  `durableagent/forensics.go`, `maintenance.go`
- Why it exists: Status, review cards, forensic sidecars, TES, logs, and repair
  commands exist, but not every operator-facing failure carries the same compact
  chain from symptom to canonical record, projection, inspect command, and code
  owner.
- Exit gate: Add a standard debug breadcrumb schema to blocked/failure/proposal
  surfaces: `trace_id`, `canonical_record`, `projection`, `inspect_command`,
  `code_owner`, and `next_repair_action`.

### DP-005: Large operational surfaces make ownership harder to inspect

- Principle: `Short paths to truth`; `Minimal stack, strong substrate`
- Status: `active`
- Surface: root command/Telegram composition files, `runtime/*`,
  `session/store.go`
- Why it exists: The stack is intentionally small, but composition, operational
  repair, runtime shell behavior, and persistence records have accumulated in
  large surfaces. That increases the number of local hops required to debug
  ownership and repair behavior.
- Exit gate: Split broad files by command, record, or runtime concern only when
  the boundary is durable and behavior-preserving. Guard the stable package
  direction with machine checks instead of introducing one-off packages.

## Contained Exceptions

### DP-006: Exact secret scanners

- Principle: `Compile contracts; interpret ambiguity`
- Status: `contained`
- Surface: `durableagent/forensics.go`, `runtime/doctor.go`
- Why it exists: Secret scanners intentionally use exact regex/string checks as
  a fail-closed safety layer for concrete token-shaped values.
- Exit gate: Keep these scanners deterministic and narrow. They are acceptable
  only when tests prove concept-only mentions do not erase useful summaries and
  concrete secret values still quarantine or redact.

### DP-007: Execution claim detectors are still lexical

- Principle: `Compile contracts; interpret ambiguity`; `Ledger, not vibes`
- Status: `active`
- Surface: `runtime/constitution_runtime.go`
- Why it exists: Final-reply grounding detects unsupported completion, tool,
  test, and durable-agent claims from text markers before comparing them to TES
  evidence.
- Exit gate: Move open-language claim detection into an interpretation contract
  that returns typed claim candidates with confidence and spans; runtime should
  validate those candidates against TES before repair.

### DP-008: Media intent still has lexical fallback

- Principle: `Text is presentation, not authority`; `Compile contracts; interpret ambiguity`
- Status: `migrating`
- Surface: `runtime/media_intent.go`
- Why it exists: audio/text reply routing now records a typed
  `InterpretationClaim` in floor metadata, but the initial same-turn and
  next-audio intent detector still accepts a narrow lexical fallback for legacy
  operator phrasing.
- Exit gate: Move media-artifact intent into the shared open-language
  interpretation lane so the runtime consumes only typed claims plus artifact
  capabilities; keep lexical matching only for closed command syntax, if any.

## Machine-Checked Paths

`make design-principles` requires these high-risk string-heavy surfaces to remain
listed here until they are retired:

- `session/authority_contract.go`
- `runtime/constitution_runtime.go`
- `runtime/continuation_materialize.go`
- `runtime/operation_phase_gate.go`
- `runtime/goal_continuation.go`
- `runtime/media_intent.go`
- `runtime/external_channel_wake.go`
- `runtime/continuation.go`
- `runtime/status.go`

New authority, consent, continuation, wake, or goal-inference code that uses
string matching must either avoid the pattern or add a debt entry with an exit
gate in the same change.

The same check now rejects retired prose-authority helper symbols
(`positiveAuthorityEffectText`, `bounded_effect_positive_clause`,
`operationPhaseApprovalText`, `inferOperationGateReasonCode`, and
`operationPhaseIsEscalatedOperatorApproval`) so they cannot be reintroduced as
authority paths.
