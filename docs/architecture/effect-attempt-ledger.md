# Effect Attempt Ledger

The effect-attempt ledger records authority-sensitive work as a durable
lifecycle, not as prose recovered from a transcript.

An effect attempt is the runtime-owned fact that a side effect may have happened:

```text
attempted -> executed | failed | uncertain -> verified | rejected | superseded
```

This is retry safety, not universal exactly-once execution. Aphelion may not
know whether a mutation succeeded, but it must durably know that it may have
happened and must verify or ask before retrying it.

`executed` means only that the executor returned without an error for the
side-effecting command. It is still retry-blocking until a verifier or explicit
resolution promotes it to `verified`, `rejected`, or `superseded`. Older
`succeeded` rows are normalized to `executed` when read.

## Boundary

- `commandeffect` classifies the command effect.
- `effectauth` decides whether current authority permits the effect.
- `session.effect_attempts` records the attempt lifecycle and evidence refs.
- Tool monitors write an `attempted` row when side-effecting `exec` starts, so a
  crash between dispatch and finish still leaves durable evidence that the
  mutation may have happened.
- Runtime completion and retry decisions consult effect attempts before
  projection fields such as `WorkResult.Commands`, patch previews, summaries, or
  raw `exec_effect` event payloads.

`exec_effect` remains useful event evidence, but it is not the canonical
completion surface. When a matching effect attempt exists, authority-sensitive
completion should not depend on previews, truncated JSON, model-authored
summaries, or conversational claims.

## Verification

Verification is effect-family specific and intentionally conservative.

- Workspace-write verification can prove bounded candidate artifacts inside the
  approved root/window.
- Git commit reconciliation can verify local commit evidence.
- Git push and external-account work require read-only verification of the
  external state when enough typed subject data and authority exist.
- Service/process status can be recorded as evidence, but status alone does not
  prove restart causality.

When a verifier is missing or inconclusive, the attempt stays `uncertain` and
automatic retry is blocked until a bounded follow-up approval resolves it.
