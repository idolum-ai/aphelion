# Cron — Scheduled Proactivity, Job Sessions, and Delivery Policy

## Overview

Cron is Aphelion's subsystem for scheduled jobs and deferred follow-up work.

Cron is not heartbeat.

- **cron** is scheduled, job-defined, and procedural
- **heartbeat** is reflective, stateful, and selective

Cron is one of Aphelion's mechanisms for **scheduled proactivity**.

From the user's perspective, cron may still feel proactive. Internally, the causal source is a standing job rather than a reflective maintenance turn.

## Scope

### Required

- persisted cron job definitions
- dedicated cron scheduler
- explicit schedule and delivery policy
- dedicated cron sessions
- isolation from heartbeat semantics
- quiet-by-default delivery rules

### Deferred

- richer recurrence tooling
- external triggers beyond local schedules
- cron-owned subagent orchestration
- advanced UI for job inspection and editing

## Core Model

Cron is a runtime-owned scheduler, not a vague model habit.

The model may create, inspect, or modify jobs through a tool interface, but:

- schedules are persisted by the runtime
- wakeups are determined by the scheduler
- delivery and retries are runtime-managed

Cron exists so Aphelion does not emulate scheduling with `sleep`, poll loops, or improvised reminders.

## Jobs

Each cron job should define:

- job identity
- schedule
- payload
- session target
- delivery policy
- enabled/disabled state
- run history

At minimum, schedule kinds should include:

- one-shot timestamp
- fixed interval
- cron expression

## Session Model

Cron runs should have their own session model.

Supported target shapes should include:

- `main`
- `isolated`
- `current`
- named custom session

### `main`

Main-target jobs enqueue work for Aphelion's main/admin-facing continuity rather than starting a fresh isolated worker session.

This may bridge into heartbeat or another governor-owned wake path, but cron and heartbeat remain distinct subsystems.

### `isolated`

Isolated jobs run in a dedicated cron session with bounded context and explicit delivery policy.

This is the default shape for background work, reports, and chores that should not contaminate the main session transcript.

### `current` or custom session

These targets are for workflows that intentionally build on an existing conversational or task continuity.

They should be explicit, not accidental.

## Context Policy

Cron sessions should use lightweight context by default.

Cron is usually executing a defined obligation, not inheriting the full soul and memory surface of the main session.

That means:

- minimal bootstrap injection by default
- explicit tool allow-list when useful
- bounded prompt context
- no silent replay of full unrelated history

This keeps cron jobs legible, cheaper, and less likely to drift.

## Delivery

Cron delivery must be explicit.

Supported modes should include:

- `none`
- `announce`
- `webhook`

Cron should default toward artifacts and events first, not chatty conversational output.

If a cron run does produce a user-visible message:

1. the scheduled job produces a governor floor
2. `Idolum` authors the user-facing scheduled message from that floor when available
3. the governor authorizes the outward message
4. the delivered message enters the visible ledger of the target session
5. the cron floor remains sidecar audit state

This preserves the same rule used elsewhere:

- visible ledger stores the delivered scene
- governor floor remains sidecar audit state

## Proactivity Model

Cron is **scheduled proactivity**.

It may feel spontaneous to the user, especially if they forgot the standing instruction that created the job.

That is acceptable, as long as the internal causal chain remains truthful.

Internally, cron-originated outreach should be auditable as:

- scheduler-originated
- job-linked
- distinct from heartbeat-originated outreach

The face layer owns the relationship-bearing scheduled wording when available, but it must not self-authorize the delivery.

The rule remains:

- `Idolum` may propose
- `Idolum (System)` ratifies

## Relationship to Heartbeat

Cron and heartbeat are siblings, not variants of the same thing.

### Cron

- scheduled
- job-defined
- procedural
- replayable from stored job state
- usually narrower in context

### Heartbeat

- reflective
- maintenance-oriented
- may choose silence
- driven by `HEARTBEAT.md` and system state

They may both produce proactive outward messages, but for different reasons.

## Authority and Security

Cron must obey the same machine-owned floor as all governor activity.

That means:

- no prompt-authored permission widening
- no bypass of sandbox policy
- no implicit authority change because something is scheduled
- job execution stays within the principal/role context assigned to the job

Isolated cron sessions should be especially careful about context and writable roots.

## Failures, Retries, and Inspection

Cron should be inspectable as an operational subsystem.

At minimum, the runtime should preserve:

- job status
- last run state
- delivery status
- failure state
- retry/backoff behavior
- run history

Cron should be resumable after restarts through its persisted job store.

## Config Surface

See `config.md`, but cron ownership should preserve room for:

```toml
[cron]
jobs = []
```

Later config may add:

- retry/backoff policy
- default delivery policy
- scheduler concurrency
- catch-up behavior
- per-job model/tool overrides

## Decisions

- **Cron is scheduled proactivity.** It creates outward initiative from stored obligations, not reflective noticing.
- **Cron is runtime-owned.** The model may manage jobs, but the scheduler owns time and execution.
- **Cron sessions are explicit.** Scheduled work should not accidentally inherit arbitrary conversational continuity.
- **Cron context is lightweight by default.** Scheduled jobs should be narrow unless they intentionally target a richer session.
- **Cron may still feel spontaneous.** User perception of proactivity does not change the internal causal source.
- **`Idolum` may propose cron outreach.** The governor still authorizes delivery.
- **Cron and heartbeat stay separate.** Shared outward effect does not mean shared internal mechanism.

## Test Plan

- **TestCronJobPersistsAcrossRestart**: stored jobs survive process restart
- **TestCronUsesDedicatedSessionForIsolatedJobs**: isolated jobs do not pollute the main session
- **TestCronMainTargetDoesNotCollapseIntoHeartbeatSemantics**: main-target cron work remains identifiable as cron-originated
- **TestCronUsesLightContextByDefault**: isolated cron jobs do not load the full workspace prompt surface
- **TestCronIdolumSuggestionStillNeedsGovernorAuthorization**: Idolum-generated outreach candidates do not bypass the governor
- **TestCronVisibleLedgerStoresDeliveredScene**: delivered cron messages enter the visible ledger as delivered scene output
- **TestCronFloorStoredAsSidecar**: cron floor is stored separately from the visible transcript
- **TestCronRetryAndFailureStatePersist**: failures and retries are inspectable across runs
- **TestCronDoesNotUseSleepLoopsForScheduling**: deferred follow-up is expressed through cron jobs rather than improvised timer loops
