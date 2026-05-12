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
