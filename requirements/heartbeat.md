# Heartbeat — Governor Maintenance Turns, Delivery, and Quiet Reflection

## Overview

Heartbeat is a first-class subsystem for periodic governor-owned maintenance turns.

Heartbeat is not cron.

- **heartbeat** is reflective, stateful, and selective
- **cron** is scheduled task execution

Heartbeat belongs to **Idolum (System)** the governor. If a heartbeat turn produces a user-visible message, `Idolum` should own the delivered relationship surface when available. If nothing is worth surfacing, heartbeat should stay quiet.

Heartbeat is one of Aphelion's mechanisms for **reflective proactivity**.

## Scope

### Required

- dedicated heartbeat subsystem
- configurable cadence
- dedicated heartbeat session
- `HEARTBEAT.md` as a dynamic prompt surface
- active-hours gating
- delivery targeting
- quiet-by-default behavior
- proactive eligibility driven by hidden-input convergence rather than timer expiration alone
- explicit separation from cron

### Deferred

- provider-specific heartbeat cost optimization
- heartbeat-triggered subagent spawning
- richer Telegram and CLI maintenance projections
- per-principal heartbeat policies beyond admin-focused defaults

## Core Model

Heartbeat is a periodic **governor maintenance turn**.

It should run as a real Aphelion turn with:

- a session identity
- assembled governor prompt context
- bounded maintenance inputs
- auditability
- optional outbound delivery

Heartbeat must not be modeled as an invisible shell script or a generic timer callback.

## Heartbeat Session

Heartbeat should run in a dedicated maintenance session, separate from ordinary user DM sessions.

The goals are:

- keep maintenance reasoning out of normal user transcripts
- preserve continuity of maintenance state across runs
- make heartbeat activity inspectable
- avoid polluting the admin DM with internal maintenance-only deliberation

This session is governor-owned. It is not a face session, and it is not a subagent session.

## Heartbeat Context

Heartbeat may read a bounded maintenance context assembled from:

- `HEARTBEAT.md`
- pending review events
- interrupted-turn recovery facts
- recent maintenance notes or prior heartbeat artifacts
- recent session-health summaries
- stale subagent/session metadata
- selected memory reflection inputs
- current runtime health information
- maintenance semantic-retrieval results

It should not read or replay entire unrelated session histories by default. Heartbeat is a maintenance turn, not a covert full-transcript batch processor.

## HEARTBEAT.md

`HEARTBEAT.md` is a dynamic governor file.

It should be used for:

- maintenance priorities
- standing reflection questions
- reminder heuristics
- delivery preferences
- guardrails for proactive behavior

If `HEARTBEAT.md` is absent or empty, heartbeat may still run for low-level maintenance, but it should avoid speculative proactive messaging.

`HEARTBEAT.md` belongs after the stable cache boundary with other dynamic files.

## Active Hours

Heartbeat should support active-hours gating.

Outside active hours, heartbeat may:

- skip entirely
- perform only internal maintenance with no delivery
- defer user-visible delivery until the next active window

The default philosophy is:

- maintenance may continue quietly
- human-facing interruption should be time-aware

## Delivery Targets

Heartbeat delivery must be explicit.

Supported targets should include:

- `none`
- `last`
- explicit admin DM target

### `none`

Run the maintenance turn and persist its internal results, but do not deliver a message.

### `last`

Deliver to the last eligible admin-facing session when heartbeat decides there is something worth surfacing.

### Explicit admin DM

Deliver to a configured admin DM regardless of recent conversational activity.

Delivery should be bounded and selective. Heartbeat should not emit routine chatter simply because a cadence elapsed.

## Delivery Semantics

If heartbeat emits an outward message:

1. the governor produces a maintenance floor
2. `Idolum` authors the user-facing outreach language for the target channel when available
3. the governor authorizes the final outward message
4. the delivered message enters the visible ledger of the target session
5. the heartbeat floor remains sidecar audit state

This follows the same rule as ordinary turns:

- visible ledger stores the delivered scene
- governor floor remains auditable sidecar state

The proactive rule is:

- `Idolum` may suggest outreach
- `Aphelion` decides whether that outreach is sent

## Session Routing Rules

Heartbeat must not route into:

- subordinate subagent sessions
- isolated non-admin user sessions by default
- arbitrary stale user conversations just because they were recent

Heartbeat is a governor maintenance facility, not a broadcast mechanism.

Its normal outward surface is the admin DM or no delivery at all.

## Review Events and Reflection

Heartbeat is the natural place to:

- digest pending review events
- batch low-urgency admin updates
- analyze work that was interrupted by a restart or crash
- reflect on stale tasks
- notice repeated failures or degrading patterns
- decide whether silence is still the right action
- retrieve semantically related memory context for contradiction checks, recurrence, and clustering

Heartbeat may summarize, notice, and surface.

Heartbeat semantic retrieval is not the same thing as live-turn semantic search.

Heartbeat retrieval should favor:

- broader chunking
- slower cadence
- thematic recurrence
- contradiction detection
- maintenance-oriented clustering

This is a maintenance retrieval mode, not an ordinary interactive tool invocation.

Heartbeat should not automatically convert everything it observes into durable shared memory. Memory mutation must remain governed by explicit policy.

Heartbeat may also create opportunities for warm or relational check-ins that would feel unnatural from the governor alone. Those outreach candidates should come from `Idolum`, but still require governor authorization.

## Relationship to Cron

Heartbeat and cron are separate subsystems.

Heartbeat proactivity should follow `hidden-inputs.md`: recurring signal, unresolved state, temporal pressure, and convergence. A timer alone is not sufficient cause for outreach.

### Heartbeat

- periodic maintenance turn
- reflective
- may choose silence
- session-aware
- prompt-driven by `HEARTBEAT.md`

### Cron

- scheduled execution of configured jobs
- procedural
- job-defined
- isolated from conversational continuity unless explicitly bridged

Cron may produce events that heartbeat later notices, but cron is not heartbeat and heartbeat is not cron.

## Authority and Security

Heartbeat is governor-owned and must obey the same machine-owned floor as any other governor turn.

That means:

- no authority widening
- no prompt-authored permission escalation
- no bypass of principal ceilings
- no bypass of sandbox rules
- no hidden mutation of global state simply because the turn was periodic

If heartbeat touches tool execution, it must do so through the same governed tool path as any other turn.

## Config Surface

See `config.md`, but heartbeat ownership should include:

```toml
[heartbeat]
enabled = true
every = "30m"
active_hours = { start = "08:00", end = "24:00", timezone = "America/New_York" }
target = "last"               # "last" | "none" | explicit admin target
```

Heartbeat model routing is owned by the first-class `/model heartbeat` slot,
not by `[heartbeat]` TOML keys. Clearing that slot restores the install's
cheap-lane default.

The implementation may later add more knobs, but the contract should preserve:

- cadence
- active-hours policy
- slot-scoped model override through `/model heartbeat`
- delivery target

## Startup Recovery

Startup recovery is a heartbeat-adjacent maintenance responsibility.

On process start, Aphelion may perform a one-shot maintenance analysis over structured interrupted-turn records before regular cadence-based heartbeat turns begin.

That recovery pass should:

1. consume machine-authored interruption facts
2. write a recovery analysis into the maintenance ledger
3. remain quiet by default unless the operator later chooses to surface it

This is not the same thing as a normal periodic heartbeat wake, but it belongs to the same governor-owned maintenance domain.

## Decisions

- **Heartbeat is a governor maintenance turn.** It belongs to `Idolum (System)`, not to cron.
- **Heartbeat is reflective proactivity.** It may surface things Aphelion noticed, not just things it was told to schedule.
- **Startup recovery belongs to maintenance.** Restart-disruption analysis should land in the maintenance ledger, not be invented ad hoc in user chat.
- **Heartbeat runs in its own session.** Maintenance continuity should not pollute user DM transcripts.
- **`HEARTBEAT.md` is dynamic.** It belongs after the stable cache boundary.
- **Silence is a valid result.** Heartbeat should not speak merely because it woke up.
- **Admin DM is the normal outward surface.** Heartbeat is for maintenance and review, not arbitrary user interruption.
- **Scene and floor stay separate.** Delivered heartbeat messages use `Idolum`; maintenance floor remains sidecar audit state.
- **`Idolum` may propose heartbeat outreach.** The governor still authorizes delivery.
- **Heartbeat must not route into subagent sessions.** Maintenance and subordinate execution are distinct concerns.
- **Cron remains separate.** Procedural scheduled jobs must not absorb the reflective maintenance role of heartbeat.

## Test Plan

- **TestHeartbeatRunsInDedicatedSession**: heartbeat creates or reuses a maintenance session rather than polluting ordinary DM sessions
- **TestHeartbeatLoadsHeartbeatMDAsDynamicFile**: `HEARTBEAT.md` is included after stable prompt sections
- **TestHeartbeatRespectsActiveHours**: outside active hours, delivery is suppressed or deferred according to policy
- **TestHeartbeatTargetNoneProducesNoOutboundMessage**: heartbeat may run without sending anything
- **TestHeartbeatTargetLastRoutesOnlyToEligibleAdminSurface**: `target = "last"` does not pick arbitrary user or subagent sessions
- **TestHeartbeatDoesNotRouteToSubagentSession**: subordinate sessions are excluded from heartbeat delivery
- **TestHeartbeatUsesIdolumForDeliveredMessage**: delivered heartbeat output is rendered through the face layer
- **TestHeartbeatIdolumSuggestionStillNeedsGovernorAuthorization**: Idolum-generated outreach candidates do not bypass the governor
- **TestHeartbeatStoresFloorAsSidecar**: maintenance floor is stored separately from visible delivered text
- **TestHeartbeatBatchesReviewEvents**: pending review events can be surfaced as a bounded admin digest
- **TestHeartbeatDoesNotAutoPersistObservedStateToMemory**: mere observation during heartbeat does not silently mutate durable memory
