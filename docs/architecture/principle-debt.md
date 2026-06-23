# Aphelion Principle Debt Ledger

_Status: normative active tracking surface._

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
- Exit gate for active debt

Status values:

- `active`: current implementation still violates or weakens the principle
- `contained`: the risk is accepted only because a bounded scanner, test, or
  safety exception contains it
- `migrating`: replacement work is already underway

## Active Debt

### PD-2026-06-causal-closure

- **Principle:** Fail closed, but stay useful; Short paths to truth; Continuity
  over productivity theater.
- **Status:** active.
- **Surface:** Approval, continuation, capability grant, durable-child wake,
  tool execution, uncertain outcome, and operator projection handoff.
- **Why it exists:** Execution-authority continuity is implemented as a
  point-of-use authority spine, but some end-to-end workflows still stop safely
  without deterministically producing the next durable operator-legible state.
  Approval, resource denial, uncertain effects, child reports, and phase
  supersession can require manual reconstruction of the next action. The first
  implementation slice now records `next_action_records` and matching
  `workflow.next_state` events for approvals, uncertain effects, resource
  preflight failures, child wakes, and supersession; the debt remains active
  until all execution species emit those records through their real transition
  paths and operator projections consume them consistently.
- **Exit gate:** After every operator decision or execution attempt, exactly one
  durable typed next-state record exists. The state vocabulary may include
  ready, blocked, verification, waiting, retry, supersession, cancellation, and
  terminal variants, but each record must name causal IDs, owner, exact next
  operation or blocker, required authority when applicable, retry semantics,
  verifier when applicable, and the operator projection to render.

### PD-2026-06-output-exposure

- **Principle:** Authority before capability; Ledger, not vibes; Operational
  legibility.
- **Status:** active.
- **Surface:** Tool result previews, evidence hydration, logs, operator
  projections, and model context.
- **Why it exists:** Command effect authorization answers whether an operation
  may run; it does not answer whether the resulting bytes, paths, config
  metadata, or diagnostic text may be shown to a given audience. Some output
  paths still rely on size-bounded previews or source-local redaction rather
  than one audience-aware exposure projection policy. The first implementation
  slice projects ordinary turn-run tool previews through an audience-aware
  policy and redacts secret-adjacent path/config metadata; the debt remains
  active until evidence hydration, logs, operator UI, external delivery, and
  privileged artifact access share the same projection contract.
- **Exit gate:** Every tool-result exposure path records or consumes a typed
  sensitivity/provenance judgment and renders an audience-specific projection:
  redacted view, digest, withheld marker, protected artifact reference, or
  privileged hydration with audit evidence.

When this section has no entries, write `None.` to mean no known uncontained
principle violation is being carried here. Temporary architecture seams belong
in the root
[`ARCHITECTURE_WAIVERS.md`](../../ARCHITECTURE_WAIVERS.md) ledger; broader
pressure that needs watchfulness but not immediate remediation belongs under
Monitored Tensions.

## Monitored Tensions

- **Scope/legibility tension:** Aphelion's implemented surface is broad enough
  that "small enough to understand" must now mean governable, composable, and
  legible under pressure rather than absolutely small. The repair path is to
  keep package ownership, capability boundaries, authority gates, evidence
  paths, and recovery surfaces explicit as the system grows. This is not a
  mandate to delete capabilities; it is a mandate to prevent platform gravity.
- **Execution-authority continuity:** Durable children, operation-plan work,
  recovery, scheduled continuations, and ordinary interactive turns can enter
  shared execution machinery. Their lease/grant/resource authority must survive
  that crossing without becoming either lost or self-asserted. The current
  point-of-use gate is covered by the conformance matrix in
  [`execution-authority-continuity.md`](execution-authority-continuity.md); the
  longer-term pressure is to avoid drifting into per-species authority copies.
- **Descriptor-relative file authority:** Native file access currently validates
  pathnames and then uses ordinary filesystem APIs. Symlink components in grant
  roots are rejected, but the remaining check/use race is a temporal authority
  seam for child-controlled workspaces. The repair path is no-follow,
  beneath-root descriptor traversal for read, write, list, and search before
  treating file grants as high-trust secret-bearing substrate.

## Machine-Checked Paths

`make design-principles` rejects live authority, consent, continuation, wake,
goal, media, or final-reply execution inference from string matching. Protocol
parsing of explicit JSON contracts and exact concrete-value safety scanners are
allowed only when they do not decide authority from prose.

The remaining exact string checks are intentionally non-authoritative: command
and callback tokens, explicit provider/transport enums, parsed contract markers,
concrete secret-shape scanners, display compactors, test fakes, and deploy
verification markers. Any new path that converts open-language prose into
authority, consent, routing, continuation, or execution facts must go through a
deliberating interpretation role that returns typed claims for runtime
validation.

`make taste` guards the largest structural hotspots so broad operational files
do not quietly grow back after behavior-preserving splits.

The same check rejects retired prose-authority helper symbols
(`positiveAuthorityEffectText`, `bounded_effect_positive_clause`,
`operationPhaseApprovalText`, `inferOperationGateReasonCode`,
`operationPhaseIsEscalatedOperatorApproval`, `detectExecutionClaims`, and
`textRequestsPendingAudioTranscription`) so they cannot be reintroduced as
authority paths.
