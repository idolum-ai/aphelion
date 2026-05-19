# Thread-Native Durable Work

_Status: exploratory architecture direction._

This note describes a possible simplification of Aphelion's durable child model:
make the thread the operator-facing unit of durable work, and treat child-agent
machinery as backing implementation only when a thread needs isolation, wake,
transport binding, or remote execution.

The goal is not to remove governance. The goal is to preserve the speed and
natural feel of `/thread` while letting the same lane progressively gain the
durable features that currently require creating and operating a separate
durable agent.

North-star shape:

> A thread is the durable unit of work; authority, wake, memory, transport, and
> isolation are capabilities attached to that thread.

## Why Consider This

Side threads have the right operator texture:

- Starting one is cheap: `/thread <message>`.
- Continuing one is obvious: reply to its messages or use `(thread N)`.
- Closing one is cheap: `/absorb N`.
- The lane remains inside the normal Telegram radio link, busy gate, turn
  router, progress state, continuation approvals, memory focus, and replay
  recovery.

Durable children have the right governance ingredients, but the operator path
is heavier:

- The user has to create and name a separate entity.
- The role, charter, bootstrap, policy, wake mode, review target, storage roots,
  and channel binding are all exposed early.
- The surface area splits between `/thread`, `/agents`, durable-agent CLI,
  child wake state, review artifacts, Tailnet control, and capability tools.
- Simple work feels brittle because the system asks the operator to manage the
  substrate before the work lane has proved it deserves that substrate.

The design pressure is clear: keep thread creation lightweight, then add durable
material only at the moment the thread actually needs it.

## Operator Surface Split

The simplest operator split is:

- `/threads`: lightweight scratch and side-work lanes.
- `/agents`: promoted durable lanes with wake, policy, access, binding, review,
  and lifecycle controls.

In this model, `/threads` should not grow a full durable control deck. It should
show the actions that belong to ordinary side threads:

```text
Thread 2: Inbox triage plan

[Continue] [Promote] [Absorb]
```

`Promote` is the explicit boundary crossing. It says this lane has become
important enough to retain, govern, wake, or bind. Promotion should preserve the
thread's useful context while creating the durable records needed for later
authority and operation.

```text
Promote Thread 2?

This keeps the lane as a durable agent with its current context.
Default policy: draft-only, ask before external action.

[Promote] [Edit Policy] [Cancel]
```

After promotion, `/agents` becomes the richer durable work board:

```text
Agents

Inbox Triage
from thread 2, draft-only, no wake, no external access

[Continue] [Wake] [Access] [Policy] [Archive]
```

This preserves the snappy thread experience and avoids asking every side thread
to carry durable controls. It also keeps a clear operator moment where the
system can explain that durable work has stronger lifecycle and authority
semantics.

The danger is merely moving today's friction behind a `Promote` button. To avoid
that, `/agents` must be thread-like and card-driven. It should hide agent IDs,
bootstrap ceilings, channel kinds, storage roots, and CLI-shaped setup details
unless the operator opens details or diagnostics.

Promotion should be continuity-preserving, not a copy-and-forget migration:

- the promoted agent keeps a short human label derived from the thread;
- the source thread keeps a backlink to the promoted agent;
- `Continue` on the agent should feel like continuing the original lane;
- the thread transcript should remain available as bounded context or evidence;
- raw backing principal IDs should stay in details, health trace, and
  maintenance surfaces.

## Proposed Vocabulary

- **Thread**: an operator-visible work lane with a durable session scope, queue,
  transcript, progress state, approvals, and lifecycle.
- **Durable thread**: a thread with one or more durable attachments such as a
  charter, schedule, capability grant, local workspace, or external binding.
- **Thread profile**: typed metadata for the thread's role: title, charter,
  memory focus, model preference, and parent scope.
- **Thread policy**: typed authority limits for the thread: outbound mode,
  autonomy, drift policy, visibility, capability envelope, and stop conditions.
- **Thread wake**: a typed trigger that can enqueue work for the thread: schedule,
  parent prompt, external adapter poll, push signal, or deploy/recovery wake.
- **Thread binding**: a compiled transport or runtime binding such as a Telegram
  group, local external adapter, Tailnet remote, or isolated child process.
- **Backing principal**: an internal principal used when a thread needs separate
  capability grants, storage, sandbox identity, or remote enrollment.
- **Promoted agent**: the operator-facing `/agents` card created from a thread
  when durable controls become useful. Internally it may be backed by a durable
  thread profile, a durable-agent record, or another backing principal.

The operator should mostly see threads. Backing principals and legacy durable
agent IDs can remain visible in details, health traces, CLI maintenance, and
forensic records.

## Operator Flows

The basic flow stays unchanged:

```text
/thread investigate inbox triage
(thread 1) draft a read-only plan
/threads
/absorb 1
```

Durability becomes progressive:

```text
(thread 1) keep this as Inbox Triage
(thread 1) wake every morning with a summary of new mail
(thread 1) request read-only mail access
(thread 1) ask me before deleting anything
```

The runtime should translate those requests into typed records:

- a thread profile for "Inbox Triage";
- a wake schedule for the morning review;
- a capability request and grant for read-only mail access;
- a thread policy that forbids destructive mail operations without approval.

If the command comes from an unpromoted side thread, the first response can be a
promotion proposal instead of immediately exposing all durable controls:

```text
Thread 1 needs durable controls for a morning wake.

[Promote And Configure Wake] [Promote Only] [Cancel]
```

External or remote work keeps the same operator shape:

```text
(thread 2) bind this to the family group as a read-only helper
(thread 3) run this on the Tailnet host aphelion-lab
```

Those requests may still materialize specialized runtime records underneath, but
the first-class operator object remains the thread.

After promotion, the operator can manage durable features from `/agents` without
memorizing specific command grammar:

```text
Inbox Triage Wake

Schedule: every morning at 8:00
Scope: Inbox Triage only
Last run: completed today 8:02
Next run: tomorrow 8:00

[Change Time] [Pause] [Run Now] [Details]
```

Buttons are projections of typed state. They should not be treated as command
shortcuts that bypass parsing or authority checks. Callback payloads should carry
canonical IDs, and every action should re-read current state before mutating it.

## Attachment Model

A durable thread can be represented as a small base lane plus optional typed
attachments:

| Attachment | Purpose | Truth class |
| --- | --- | --- |
| Thread row | Open/closed state, display slot, created text, absorb summary | operational current-state store |
| Thread session | Transcript, floor sidecars, plan state, operation state | canonical or operational per field |
| Thread profile | Durable role, title, charter, memory focus, model preference | canonical |
| Thread policy | Autonomy, outbound mode, drift policy, visibility, capability envelope | canonical |
| Thread wake | Schedule, queue, retry/backoff, last attempt/result | operational current-state store |
| Thread binding | Telegram group, adapter, isolated process, Tailnet remote | canonical for declared binding |
| Capability records | Requests, reviews, grants, invocations | canonical |
| Execution events | Runtime evidence for wake, delivery, tool use, failure, recovery | canonical |

The important constraint is that text remains presentation. A message like
"wake every morning" proposes a durable change; it does not become authority
until compiled into a schedule, lease, grant, or other typed record.

## Mapping To Current Code

Current `/thread` already has most of the lane mechanics:

- `session.telegram_threads` tracks open and absorbed side-thread state.
- `session.TelegramThreadScopeRef` gives each thread its own durable session
  scope.
- Telegram command routing can create, target, summarize, and absorb thread
  lanes.
- Thread-targeted work already passes through busy decisions, artifact retention,
  continuation approvals, progress state, replay recovery, memory focus, and
  scoped auto-approval.

Current durable children already have most of the durable attachments:

- `session.durable_agents` stores child identity/config.
- `session.durable_agent_state` stores policy handshake and runtime posture.
- Durable wake adapters synthesize scheduled, parent-conversation, Codex
  app-server, and external-channel turns.
- Durable children use child-scoped sandboxes, storage roots, principal-scoped
  tools, review artifacts, and Tailnet control-plane records.

The proposed direction is to stop treating those as separate operator worlds.
Instead, durable-agent records can become backing records for threads that need
features not available to a plain local side thread.

## Design Rules

- A plain thread must stay cheap to create and close.
- Durable features attach progressively; they must not be required for ordinary
  side-thread work.
- `/threads` should remain a lightweight work-lane board. Rich durable controls
  belong in `/agents` after promotion.
- Promotion must be one-tap for the default safe path and editable when policy
  details matter.
- Thread labels remain human-scale. Raw IDs stay in trace/detail surfaces.
- Every durable attachment has a typed record. Prose can propose; it cannot
  authorize.
- Capability expansion still goes through request, review, grant, expiry,
  revocation, and invocation evidence.
- Transport bindings are compiled, narrow adapters. This must not become an
  omnichannel plugin layer.
- Remote or isolated execution still needs a hard principal, storage boundary,
  bootstrap ceiling, and control-plane evidence. The operator can see it as a
  thread-backed runtime binding, not as a separate everyday object.
- Absorbing or closing a durable thread must have explicit semantics for wakes,
  bindings, pending approvals, capability grants, and remote enrollment.

## Migration Shape

This should not be a flag-day deletion of durable agents. A safer sequence:

1. Document the thread-native model and review it against design principles.
2. Improve projections so `/threads`, `/status`, and `/health trace` can show
   durable attachments for a thread without changing authority semantics.
3. Add a `Promote` action to the thread projection that creates a default safe
   durable profile from the thread and links the two surfaces.
4. Make `/agents` render promoted durable work as cards with state-derived
   buttons for continue, wake, access, policy, details, and archive.
5. Add a canonical thread-profile/policy surface for local durable threads.
6. Let new local or scheduled durable work attach to a thread profile before it
   creates any backing durable-agent record.
7. Introduce an internal backing-principal link for cases that need isolated
   sandbox identity, capability grants, or Tailnet enrollment.
8. Project legacy durable-agent records as backing principals attached to
   operator-visible threads where possible.
9. Keep durable-agent CLI commands for maintenance and migration until the new
   thread-native surfaces cover the operational need.

## Non-Goals

- Do not remove typed authority records.
- Do not weaken child-scoped credentials, storage roots, or sandboxing.
- Do not replace Tailnet node identity checks with thread names or chat text.
- Do not make `/thread` a generic plugin marketplace.
- Do not auto-write memory merely because a thread was absorbed or made durable.
- Do not hide forensic identifiers from maintenance and diagnosis surfaces.

## Open Questions

- Should the long-term canonical thread identity be Telegram-specific, or should
  Aphelion introduce a transport-neutral work-thread table with Telegram display
  slots as a projection?
- What is the exact principal string for grants to a durable thread:
  `thread:<id>`, `telegram_thread:<chat>:<id>`, or a backing durable principal?
- Should a thread profile be allowed without a backing principal, or should every
  durable thread immediately receive a backing principal record?
- What does `/absorb` mean for a scheduled thread: pause wakes, revoke wakes, or
  ask the operator?
- Should `Promote` close the source thread, leave it open as an alias, or ask
  after the promoted agent is created?
- Should `/agents` replace `/threads` for promoted work entirely, or should
  `/threads` show promoted backlinks in a compact read-only form?
- How should group-bound threads be represented when the Telegram group itself
  has a durable transcript and independent reply policy?
- Which legacy durable-agent fields belong directly on thread profile/policy,
  and which should remain only on backing runtime records?

## Review Criteria

The direction is working if:

- a new durable workflow starts as quickly as a side thread;
- adding wake, memory focus, or a capability feels like extending the current
  lane, not switching products;
- `/threads` stays compact, with promotion as the main bridge to durable
  controls;
- `/agents` becomes a state-driven durable work board rather than a setup-heavy
  registry;
- `/status` and `/health trace` can point from visible thread to typed authority
  and execution evidence in one hop;
- ordinary threads do not inherit ambient capability;
- remote children remain strongly identified and bounded even when presented as
  thread-backed work.
