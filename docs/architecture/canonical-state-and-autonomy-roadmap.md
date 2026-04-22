# Canonical State and Autonomy Roadmap

_Status: draft design memo._

This document is not the normative spec. It is a working architecture note for
where Aphelion should converge if the goal is a safer, more autonomous system
with durable continuity, bounded sensory organs, and self-correcting behavior.

It is written from the perspective that recent TES work is the right direction,
but that the system still needs a clearer answer to a deeper question:

**Which surfaces are canonical truth, which are projections, which are
operational current-state stores, and which are compatibility fallbacks on the
way out?**

That question matters because autonomy fails long before capability fails. A
system can only safely self-direct, self-debug, and later self-improve if it
knows where to look for the truth of what happened, what it is now, and what it
has learned over time.

## Directional Goal

The long-range aim is not just a more convenient assistant. It is an agentic
system that can:

- preserve long-term continuity without improvising its own history
- remain legible to its operator under long-running, multi-surface work
- quarantine hostile or low-trust context through bounded sensory organs
- retain memory without turning memory into an unstructured blob
- debug its own behavior against execution evidence
- deliberate against drift instead of merely reacting in character
- eventually propose and implement bounded self-improvements safely

That implies a stronger distinction among:

1. **what happened**
2. **what is true now**
3. **what was learned and retained**
4. **what is shown to the operator**

If these collapse into one surface, autonomy becomes theater.

## Canonical Surfaces

### 1. TES is canonical for execution truth

The Transparent Execution Sequence should become the canonical append-only record
for runtime facts.

Canonical scope:

- ingress acceptance, queueing, compaction, selection
- turn start / stage progression / terminal outcome
- tool attempts and results
- delivery attempts and outcomes
- continuation lifecycle
- decision lifecycle
- recovery lifecycle
- durable-child lifecycle and parent/child coordination facts

What TES answers:

- what actually happened
- in what order it happened
- which evidence supports a later claim
- whether a summary, quick-read, or explanation is grounded

Design stance:

- TES should be the first place runtime truth is written
- projections may summarize TES, but should not silently compete with it
- if a surface contradicts TES, the surface is wrong

### 2. Identity/config is canonical for mutable present state

Identity/config surfaces should be canonical for **what an agent currently is**.

This includes, for a parent or child:

- charter and role framing
- bootstrap / substrate / model configuration
- capability envelope and policy posture
- wake mode / transport posture
- current durable channel configuration
- current continuity-facing self-description

Design stance:

- identity is mutable current-state description
- bootstrap changes should update identity/config, not rewrite memory
- operator inspection of "what is this agent now?" should read from identity
- migration decision now enforced:
  - `session.durable_agents` is canonical identity/config for durable children.
  - `session.durable_agent_state` is split by meaning:
    - identity/config-bearing fields are canonical identity/config.
    - runtime/apply/transient posture fields remain operational current-state.
  - no separate identity store is planned unless this split proves insufficient.

This is especially important for durable children. If a child's substrate changes,
its identity surface should reflect that immediately and explicitly.

### 3. Memory is canonical for retained historical meaning

Memory should be canonical for retained meaning across time, not for mutable
configuration.

Memory should answer:

- what durable facts have been retained
- what decisions were made and why
- what open questions remain alive
- what patterns matter across sessions

Design stance:

- memory is historical and append-oriented
- memory should not be rewritten to pretend a new substrate was always true
- substrate/config changes should append continuity events, not retroactively
  re-author the memory record

For children, this means:

- memory can record: "bootstrap changed from X to Y for reason Z"
- memory should not become the mutable source of current bootstrap truth

### 4. The transcript is canonical for user-visible interaction history

The visible session transcript remains canonical for what was actually shown to a
human or received from one.

This includes:

- user-visible messages
- delivered assistant replies
- operator-visible review artifacts and relays

Design stance:

- transcript is not the whole mind
- transcript should never be treated as the sole execution record
- transcript and TES must remain distinguishable, because scene text is not the
  same thing as operational truth

## Projected Surfaces

These surfaces are important, but should be understood as projections rather than
primary truth stores.

### `/status` and `/debug`

These should increasingly become **TES-backed projections**.

They are where operators read the system, but they should not invent their own
state models beyond what is needed for presentation.

### Sidecars and runtime summaries

Operation, plan, hidden-input, delivery, and turn quick-reads should be treated
as **derived presentation surfaces**.

They may cache or summarize, but the architecture should assume they are
replaceable projections from canonical truth.

### Durable review artifacts

These are bounded upward summaries, not canonical state. They matter because they
shape governance and operator decisions, but they should not be treated as the
source of execution truth.

## Compatibility Fallback Surfaces

Some existing surfaces are still operationally necessary but should be treated as
compatibility fallbacks unless promoted explicitly.

### `turn_runs`

`turn_runs` still has value for recovery and operational speed, but directionally
it should become one of:

- a projection or operational current-state store derived from TES for fast access, or
- a narrowed recovery-specific surface with less architectural authority

It should not remain a peer truth system indefinitely.

### persisted status snapshots / cached phase fields

These are useful as fallback or performance aids, but they should not retain
independent semantic authority once TES coverage is sufficient.

### legacy continuation and pending-decision surfaces

These should move toward:

- canonical event truth in TES
- operational current-state stores where needed
- compatibility fallback only where migration is incomplete

## Working Surface Classification Matrix

This matrix is a working map of live surfaces and intended classification. It is
for review and migration planning; once ratified, equivalent entries should move
to normative docs.
Classifications use the shared truth classes from
[`docs/architecture/README.md`](./README.md).

| Surface / Store | Classification | Canonical Question | Notes |
| --- | --- | --- | --- |
| `session.execution_events` | canonical | What happened, in what order? | Append-only runtime facts for ingress/turn/tool/delivery/control and durable lifecycle. |
| `session.messages` | canonical | What scene text was recorded for the session? | User/assistant/tool scene ledger. |
| `messages.floor_content` | canonical | What floor text was captured alongside scene text at message-record time? | Canonical floor-text payload attached to scene records. |
| `messages.floor_metadata` | canonical | What floor metadata/artifact references were captured alongside scene text at message-record time? | Canonical structured floor payload attached to scene records. |
| `session.outbound_messages` | canonical | Which outbound deliveries were recorded at the transport ledger level? | Canonical delivery ledger only; not proof that a human client rendered/read the message. |
| `session.review_events (status='delivered')` | canonical | Which bounded review artifacts were shown to humans? | Delivered review artifacts are part of transcript history. |
| Parent/child memory files and `rhizome_*` tables | canonical | What was retained over time and why? | Durable historical meaning (facts, decisions, questions, patterns). |
| `session.durable_agents` | canonical | What durable-child identity/config is currently declared? | Mutable child identity and capability envelope. |
| `session.durable_agent_state (identity/config-bearing fields)` | canonical | Which child identity/config handshake facts are currently declared? | Canonical child control-plane identity/config handshake state. |
| `session.durable_agent_state (runtime/apply/transient posture fields)` | operational current-state store | What durable-child runtime/apply state is currently declared? | Live child runtime and policy-apply posture. |
| `sessions.last_floor_text` | operational current-state store | What floor text is currently declared for the active session? | Latest floor-text snapshot used for current turn context. |
| `sessions.last_floor_metadata` | operational current-state store | What floor metadata is currently declared for the active session? | Latest floor-metadata snapshot used for current turn context. |
| `sessions.plan_state_json` | operational current-state store | What plan intent is currently declared? | Runtime-declared plan state for the active session. |
| `sessions.operation_state_json` | operational current-state store | What operation intent/stage is currently declared? | Runtime-declared operation state for the active session. |
| `pending_decisions` | operational current-state store | What decisions are currently pending and actionable? | Live governance queue authority until TES-only control projections fully replace it. |
| `sessions.continuation_state_json` | operational current-state store | What continuation state is currently declared? | Live continuation budget/offer state for the active session. |
| `session.review_events (status='pending')` | operational current-state store | Which review artifacts are queued for governance delivery? | Pending-review queue state before delivery. |
| `/status`, `/debug`, quick-read render blocks, Telegram status posts | projection | How should current state be presented now? | Presentation surfaces; should remain TES-backed and deterministic on conflict. |
| Turn sidecars (`turn.sidecars.captured`) and plan/operation overlays | projection | What concise operator context should be surfaced? | Derived summarization; replaceable if stronger projection exists. |
| `turn_runs` | compatibility fallback | What recovery/runtime hints are available quickly? | Transitional recovery surface; should not be co-equal canonical execution truth. |

## Canonicality Rules

A simple rule set would help keep the architecture coherent.

### Rule A — append-only facts go to TES

If a thing happened in runtime and should later be auditable, it belongs in TES.

### Rule B — mutable current-state description goes to identity/config

If a thing describes what an agent currently is or how it is currently wired, it
belongs in identity/config.

### Rule C — retained meaning goes to memory

If a thing is worth remembering across time as a fact, decision, question, or
pattern, it belongs in memory.

### Rule D — operator-facing surfaces are projections unless explicitly marked otherwise

If a thing exists to render status, debug, summaries, prompts, or overlays, it
should be treated as a projection by default.

## What This Implies for Durable Children

Durable children are bounded organs of a larger house. Some may be user-facing,
but they remain subordinate to parent governance scope and policy boundaries.

That implies three distinct surfaces for each child:

1. **identity/config** — what this child is now
2. **memory** — what this child has retained historically
3. **execution truth** — what this child has actually done

This separation matters for safety.

If a child receives hostile context from email, web pages, or a group chat, the
operator should be able to distinguish:

- whether the child was exposed
- what the child actually did
- whether the child changed in any durable sense
- whether that durable change was ratified upstream

Without that separation, sensory-organ isolation is only partial.

## Sensory Organs and Trust Boundaries

The sensory-organ idea is the right one.

External surfaces like inboxes, browsing, PDFs, and group channels should be
handled through bounded subordinate organs rather than direct exposure of the
core parent.

Those organs should be:

- prompt- and tool-bounded
- continuity-scoped
- inspectable through TES and review artifacts
- unable to durably rewrite the parent on their own authority

This is not only about security. It is also about trust recovery.

If the system is meant to remain reliable under messy or adversarial human
context, it needs organs that can absorb exposure without collapsing the whole
house into that exposure.

## Memory Management Beyond Long Context

Long context is not enough.

A more autonomous Aphelion will need explicit memory management along at least
four lines:

- **working context**: what matters for this turn
- **identity continuity**: what the system currently is
- **durable memory**: what is retained across time
- **execution evidence**: what actually happened during operation

These should not be flattened into one store.

If they are separated cleanly, memory stops being a dumping ground and becomes
part of governance.

## Self-Debugging and Drift Resistance

Safer autonomy requires more than a constitution pass over final replies.

Aphelion should eventually be able to:

- compare outgoing claims against TES evidence
- inspect what it told others versus what actually happened
- detect when projection layers drift from canonical truth
- notice when memories, identity surfaces, and execution history disagree
- use bounded internal review loops to critique operational performance

That is the beginning of genuine self-debugging.

The important point is that this should happen through **structured evidence
surfaces**, not vague self-reflection alone.

## Deliberation and the "big hammer of mind"

If the goal is resistance to drift, there likely needs to be a stronger internal
review/deliberation layer that can inspect the whole house at a higher level.

That layer should not be a generic second model pass with unclear authority. It
should be bounded by explicit questions such as:

- what do TES, identity, memory, and transcript each claim here?
- where do those claims diverge?
- is the user-visible summary stronger than the evidence?
- is a child asking for durable change through the right governance channel?
- what should be escalated versus merely observed?

In other words: deliberation should operate as a structured anti-drift hammer,
not just extra prose generation.

## Self-Improvement

Eventually, bounded self-improvement becomes imaginable only after the layers
above are stable.

A safe progression looks like:

1. inspect execution truth
2. critique failures against evidence
3. propose bounded changes
4. require ratification where the change affects capability, privacy, or trust
5. preserve provenance of what changed and why

Without TES, identity separation, and governed memory, "self-improvement" would
mostly mean untraceable drift.

## Recommended Near-Term Sequence

### 1. Finish naming canonical vs projection vs operational current-state vs compatibility fallback surfaces

Do this explicitly in architecture docs and code comments.

Every major surface should answer one of:

- canonical
- projection
- operational current-state store
- compatibility fallback

### 2. Complete TES-first projection migration

Push more visible/runtime reads toward TES, especially where status, progress,
continuation, and debug still overlap with older stores.

### 3. Implement identity-vs-memory continuity for durable children

Bootstrap/substrate changes should:

- update child identity/config state
- append a continuity event to memory
- never rewrite retained memory as if the new substrate were always true

### 4. Strengthen self-debugging surfaces

Build bounded read paths that let Aphelion inspect:

- recent TES evidence
- visible transcript claims
- identity/config state
- durable memory deltas

### 5. Expand sensory-organ capability gradually

Only once the above is stable should more aggressive organs get broader browsing,
mailbox mutation, or higher-autonomy channels.

## Specific Architectural Preference

If one thing must be remembered from this document, it is this:

**TES should be canonical for what happened. Identity should be canonical for
what the system is now. Memory should be canonical for what the system has
retained. Everything else should have to declare itself as projection,
operational current-state store, or compatibility fallback.**

That is the boundary set most likely to support real autonomy without losing
truthfulness, inspectability, or safety.
