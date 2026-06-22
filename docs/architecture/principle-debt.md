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

None.

`None.` means no known uncontained principle violation is being carried here.
Temporary architecture seams belong in the root
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
