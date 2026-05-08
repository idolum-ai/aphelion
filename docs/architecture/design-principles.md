# Aphelion Design Principles

_Status: normative design direction._

Aphelion is a minimal, governed outpost for personal agents. It is built for
distance: distance from a laptop, distance across time, distance across trust
boundaries, and distance between intention and action.

These principles define the shape of the system. They are not feature marketing.
When implementation choices conflict, prefer the option that preserves the
outpost: small, durable, legible, recoverable, and governed by explicit
authority.

## Short Form

- Outpost, not platform.
- Radio link, not omnichannel.
- Ledger, not vibes.
- Small service, not marketplace.
- Continuity, not productivity theater.
- Authority before capability.

## Principles

### Outpost, not platform

Aphelion should stay small, durable, and operationally useful. It is not trying
to become a generic agent marketplace, IDE, enterprise automation suite, or
multi-channel assistant.

Favor narrow, dependable mechanisms over broad extension surfaces unless the
extension is required for live personal-agent operation.

### Radio link, not omnichannel

Telegram is the primary control link to the outpost. It should feel like a clear
operator channel for live work, approvals, status, recovery, and evidence.

Other adapters may exist when they serve a concrete governed use case, but the
architecture should not drift into channel abstraction for its own sake.

### Authority before capability

The system should know what it is allowed to do before it tries to become more
capable.

Capability discovery, child-agent growth, external tools, account access,
deploys, restarts, public contact, and private-data handling must pass through
typed authority records instead of relying on prose, prompt convention, or
implicit model confidence.

### Ledger, not vibes

Proposals, leases, grants, consent subjects, auto-approval budgets, revocation,
expiry, consumption, execution evidence, and recovery state should be typed
records. User-facing messages and buttons are projections of those records.

The ledger is the source of truth. Text is presentation.

### Text is presentation, not authority

Persona language can be alive, concise, and flexible. The runtime must not
depend on string matching, ritual phrases, or hardcoded message interpretation
as the source of permission.

If the persona or governor needs authority, they should create or consume a
structured contract. If they choose to say nothing, the logs and state should
still remain coherent.

### Bounded action

Every meaningful action should have a bounded effect: scope, allowed resources,
forbidden resources, consent subject, TTL, turn or action budget, validation
gates, and stop conditions.

The boundary should be readable by the operator and enforceable by the runtime.

### Consent is real

Operator approval, admin authority, resource-owner consent, third-party opt-in,
parent-principal endorsement, and system invariants are distinct concepts.

Auto-approval can reduce friction, but it cannot erase consent subjects or
override hard safety boundaries.

### Continuity over productivity theater

Aphelion should remember, resume, park work during deploys, recover after
restarts, and explain what happened. It should not pretend progress occurred
when evidence is absent.

Continuations are valuable when they preserve intent and evidence, not when they
create ritual approval churn.

### Fail closed, but stay useful

Provider failures, stale callbacks, missing durable children, expired leases,
bad grants, interrupted tools, and restart recovery should become clear,
recoverable states. They should not wedge the service, silently widen authority,
or leave the operator guessing.

Failing closed should still produce a useful next step when one is available.

### Persona and governor are collaborators

The persona is not merely a skin over the governor, and the governor is not a
script that forces the persona through brittle phrasing. The model side should
be allowed to reason, ask for context, and make interpretive judgments.

The deterministic runtime should preserve contracts, evidence, authority, and
recovery boundaries. The healthier design is argumentation plus typed contracts,
not string-heavy control.

### Operational legibility

`/status`, `/doctor`, `/debug`, logs, TES, work evidence, and Telegram controls
should make the system inspectable without burying the operator in raw IDs,
verbose ritual text, or implementation noise.

Operator-facing names should be human scale. Raw IDs can remain in details,
debug surfaces, and canonical records.

### Minimal stack, strong substrate

Aphelion should prefer a simple Linux service, Go binary, SQLite/session state,
file-based memory, scoped tools, typed execution events, and explicit install
and restart paths.

Abstractions are welcome only when they preserve clarity, reduce real
duplication, or support a concrete governed workflow.

## Implementation Bias

When adding or changing behavior:

- Prefer typed records over interpreting prose.
- Prefer projections over duplicate truth stores.
- Prefer recovery paths over irreversible failure states.
- Prefer explicit consent subjects over broad approval wording.
- Prefer concise operator text backed by detailed evidence.
- Prefer narrow tools and leases over ambient capability.
- Prefer stable service behavior over theatrical caution.
- Prefer testable invariants over prompt-only expectations.

