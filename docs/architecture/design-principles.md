# Aphelion Design Principles

_Status: normative design direction._

Aphelion is a governed outpost for personal agents. It is built for distance:
distance from a laptop, distance across time, distance across trust boundaries,
and distance between intention and action.

These principles define the shape of the system. They are not feature marketing.
When implementation choices conflict, prefer the option that preserves the
outpost: locally operable, durable, legible, recoverable, and governed by
explicit authority.

## Short Form

- Outpost, not platform.
- Radio link, not omnichannel.
- Ledger, not vibes.
- Small service, not marketplace.
- Continuity, not productivity theater.
- Authority before capability.
- Compile contracts; interpret ambiguity.
- Short paths to truth.

## Principles

### Outpost, not platform

Aphelion is no longer small in the absolute sense. Its implemented surface is
broad enough that "small" must mean governable, composable, and legible relative
to a general-agent platform, not minimal LOC or a tiny feature list.

It is still an outpost when every surface remains locally operable,
authority-bounded, evidence-producing, and diagnosable from the operator's
control room. It stops being an outpost when capability turns into ambient
platform gravity: open-ended channels, invisible authority, marketplace-style
extension, or behavior that cannot be inspected and stopped.

Favor narrow, dependable mechanisms over broad extension surfaces unless the
extension is required for live personal-agent operation and has an explicit
authority boundary, evidence path, and repair path.

### Radio link, not omnichannel

Telegram is the primary control link to the outpost. It should feel like a clear
operator channel for live work, approvals, status, recovery, and evidence.

Other adapters may exist when they serve a concrete governed use case, but the
architecture should not drift into channel abstraction for its own sake. New
channels should be compiled-in code changes behind a small transport boundary,
not plugins, marketplaces, or a second operator surface.

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

### Compile contracts; interpret ambiguity

Do not use brittle string matching as an authority layer, intent detector, or
safety classifier. It has no real understanding of edge cases and tends to fail
exactly where operator trust matters most.

Closed contracts should be parsed and checked deterministically: JSON fields,
enum values, typed records, IDs, scopes, timestamps, leases, grants, and TES
events either compile against the expected shape or they do not.

Open language should be interpreted by a layer that can disambiguate, ask for
context, and return typed claims or proposed actions. The runtime can then
validate those claims against contracts and evidence.

Unknown edge cases are expected. The answer is not to pretend every phrase can
be exhaustively matched; the answer is to preserve enough structure, evidence,
fallbacks, and emergency protocols that the system can stop, ask, recover, or
escalate cleanly when interpretation is uncertain.

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

Aphelion-shaped self-healing is not "keep trying forever." It should continue
when durable state and authority support continuation; otherwise it should
repair, rescope, park, or ask through the right surface.

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

`/status`, `/health diagnose`, `/health trace`, logs, TES, work evidence, and Telegram controls
should make the system inspectable without burying the operator in raw IDs,
verbose ritual text, or implementation noise.

Operator-facing names should be human scale. Raw IDs can remain in details,
trace surfaces, and canonical records.

### Short paths to truth

Debugging should require as few hops as possible. When something fails, the
operator and maintainer should be able to move from the visible symptom to the
responsible contract, event, code path, local artifact, and next repair action
without guessing which subsystem owns the truth.

Every operator-facing failure should carry or point to a compact chain of
evidence: what happened, what state was read or written, where the canonical
record lives, which projection rendered it, and what command or surface can
inspect the deeper detail.

The system should avoid "you might find it somewhere in logs, DB rows, sidecars,
Telegram messages, or memory files" as an implicit debugging model. If multiple
surfaces are necessary, the first surface should name the next one explicitly.

### Minimal stack, strong substrate

Aphelion should prefer a simple Linux service, Go binary, SQLite/session state,
file-based memory, scoped tools, typed execution events, and explicit install
and restart paths.

Abstractions are welcome only when they preserve clarity, reduce real
duplication, or support a concrete governed workflow.

## Implementation Bias

When adding or changing behavior:

- Prefer typed records over interpreting prose.
- Prefer exact parsing for closed contracts and LLM interpretation for open
  language.
- Prefer projections over duplicate truth stores.
- Prefer recovery paths over irreversible failure states.
- Prefer explicit consent subjects over broad approval wording.
- Prefer concise operator text backed by detailed evidence.
- Prefer narrow tools and leases over ambient capability.
- Prefer stable service behavior over theatrical caution.
- Prefer emergency stop/ask/escalate paths over brittle edge-case matching.
- Prefer one-hop trace affordances over scattered forensic scavenging.
- Prefer testable invariants over prompt-only expectations.
