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
- Status: `contained`
- Surface: `session/types.go`, `runtime/continuation_render.go`
- Why it exists: `ActionProposal`, `ContinuationLease`, operation phases, bundle
  phases, and plan leases now carry explicit `operator_title`/`plan_title`
  fields. Rendering consumes those fields first. Legacy summary-derived labels
  remain only as display fallback for old persisted records.
- Exit gate: Keep title fallback display-only. Do not let rendered labels affect
  approval, authority, lease class, plan hash, or execution scope.

### DP-004: Short debug paths are not yet universal

- Principle: `Short paths to truth`; `Operational legibility`
- Status: `migrating`
- Surface: `runtime/status.go`, `runtime/turn.go`, `runtime/doctor.go`,
  `durableagent/forensics.go`, `maintenance.go`
- Why it exists: `core.DebugBreadcrumb` now has the standard schema, and
  continuation/review-event surfaces include canonical record, projection,
  inspect command, code owner, and next repair action. Some status, doctor,
  durable forensics, and maintenance failure surfaces still need the same
  projection.
- Exit gate: Add a standard debug breadcrumb schema to blocked/failure/proposal
  surfaces: `trace_id`, `canonical_record`, `projection`, `inspect_command`,
  `code_owner`, and `next_repair_action`.

### DP-005: Large operational surfaces make ownership harder to inspect

- Principle: `Short paths to truth`; `Minimal stack, strong substrate`
- Status: `migrating`
- Surface: root command/Telegram composition files, `runtime/*`,
  `session/store.go`
- Why it exists: The stack is intentionally small, but composition, operational
  repair, runtime shell behavior, and persistence records have accumulated in
  large surfaces. A first structural pass split SQLite storage by durable record
  family and split continuation/status runtime helpers by concern, but root
  composition and several operational files still need smaller ownership cuts.
- Exit gate: Split broad files by command, record, or runtime concern only when
  the boundary is durable and behavior-preserving. Guard the stable package
  direction with machine checks instead of introducing one-off packages. Retire
  this debt when the remaining root/runtime/session hotspots are below their
  structural taste caps and the caps have stayed green through normal feature
  work.

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
- Status: `contained`
- Surface: `runtime/constitution_runtime.go`
- Why it exists: Final-reply grounding uses a narrow lexical safety scanner only
  to produce typed `InterpretationClaim` candidates for completion, tool, test,
  and durable-agent claims. Runtime validates those candidates against TES before
  repair or neutralization.
- Exit gate: Keep this as a fail-closed evidence guard. If broader claim
  interpretation is needed, move it behind role-differentiated model
  deliberation that emits typed claims before runtime validation.

### DP-008: Media intent still has lexical fallback

- Principle: `Text is presentation, not authority`; `Compile contracts; interpret ambiguity`
- Status: `contained`
- Surface: `runtime/media_intent.go`
- Why it exists: persisted next-audio continuation now requires the typed
  `InterpretationClaim` in floor metadata; summary prose is ignored. Same-turn
  operator shorthand still uses a narrow lexical scanner to create that typed
  claim for legacy Telegram phrasing.
- Exit gate: Keep persisted behavior claim-driven. Replace same-turn shorthand
  with the shared model interpretation lane if/when open-language media routing
  broadens beyond the current narrow scanner.

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

`make taste` guards DP-005's largest structural hotspots so behavior-preserving
splits do not quietly grow back into broad files.

The same check now rejects retired prose-authority helper symbols
(`positiveAuthorityEffectText`, `bounded_effect_positive_clause`,
`operationPhaseApprovalText`, `inferOperationGateReasonCode`, and
`operationPhaseIsEscalatedOperatorApproval`) so they cannot be reintroduced as
authority paths.
