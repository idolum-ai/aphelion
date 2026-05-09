# Mission Ledger Roadmap

_Status: draft design memo._

This document turns the conversational design principle **ledger, not hunger**
into an implementation plan for Aphelion.

The point is not to make Aphelion want more things. The point is to let it
remember meaningful directions across time while preserving the authority floor.
A mission may shape attention. A mission may resurface for review. A mission may
not silently expand permission.

## Core Invariant

> Remembering a mission may influence attention. Only an approved contract may
authorize action.

Corollaries:

- Automatic working objectives are allowed for current-task clarity.
- Durable missions require explicit creation, promotion, or ratification.
- Self-summon means "this is relevant enough to review," not "I may act now."
- Self-continuation is a separate authority grant with budget, scope, cadence,
  and evidence requirements.
- Mission state must be visible to `/doctor`, recovery, and review surfaces.
- No mission may bypass proposal, capability, child-agent, file/network, account,
  local-device, purchase, or public-surface gates.

## Concept Split

Do not collapse these surfaces.

### 1. Working Objective

A `WorkingObjective` is ephemeral current-task framing. It can be inferred from
most substantive user requests and used for status, progress labels, completion
checks, and restart handoff.

Example:

> Inspect Codex `/goal` and assess whether Aphelion should adapt it.

A working objective is task-scoped and replaceable. It should not become durable
memory by default.

### 2. Mission Ledger Entry

A mission is a durable remembered trajectory. It can resurface later, but by
default it has no authority to act.

Example:

> Lighthouse rename migration — candidate — review only.

A mission records objective, posture, evidence, next possible action, authority,
budget, decay, provenance, and optional overlays such as pinned or recurring.

### 3. Authorized Continuation

Authorized continuation is explicit permission for Aphelion to keep working
without a fresh user message, inside bounded constraints.

Example:

> A nightly reflection mission may run once nightly, local/private only, no network, one artifact,
> morning trace, fixed runtime budget.

A mission can be the reason continuation is offered. It is not itself the grant.

## Truth Classes

Mission Ledger should respect the architecture truth-class vocabulary:

- **Canonical**: `mission_ledger`, `mission_events`, and mission handoff/result
  rows for durable mission state, mission lifecycle facts, and mission-specific
  restart/recovery evidence.
- **Operational current-state store**: current `WorkingObjective`, active
  operation state, current plan, and continuation state.
- **Projection**: `/mission`, `/status`, `/doctor`, Telegram cards, and morning
  traces.
- **Compatibility fallback**: old operation/plan/session fragments while the
  ledger is being introduced.

If a projection claims a mission continued, completed, or failed, it must point
at mission/TES/recovery evidence rather than infer from chat transcript shape.

## Posture, Pins, and Recurrence

Do not model `pinned` and `recurring` as mutually exclusive statuses. They are
overlays on top of the mission's current posture.

A mission can be:

- `active` and pinned
- `blocked` and pinned
- `candidate` and pinned
- `active` and recurring
- `completed` and retained for later archive

Primary posture answers: **what is the mission's current lifecycle state?**
Pinned answers: **should this be retained and resurfaced across time?**
Recurring answers: **is there a scheduled/cadenced review or execution window?**

This avoids the first modeling trap in the Codex-style design: treating one
thread goal as the whole lifecycle. Aphelion needs a ledger with overlays.

## State Model

```go
type MissionStatus string

const (
    MissionStatusDormant   MissionStatus = "dormant"
    MissionStatusCandidate MissionStatus = "candidate"
    MissionStatusActive    MissionStatus = "active"
    MissionStatusBlocked   MissionStatus = "blocked"
    MissionStatusCompleted MissionStatus = "completed"
    MissionStatusExpired   MissionStatus = "expired"
    MissionStatusArchived  MissionStatus = "archived"
)
```

Suggested durable shape:

```go
type MissionState struct {
    ID        string        `json:"id"`
    Title     string        `json:"title"`
    Objective string        `json:"objective"`
    Origin    string        `json:"origin"` // user_explicit, proposed, recurring, migrated
    Scope     string        `json:"scope"`  // session, principal, child_agent, system
    Owner     string        `json:"owner"`
    Status    MissionStatus `json:"status"`

    Pinned     bool               `json:"pinned"`
    Recurrence *MissionRecurrence `json:"recurrence,omitempty"`

    CreatedAt      time.Time `json:"created_at"`
    UpdatedAt      time.Time `json:"updated_at"`
    LastTouchedAt  time.Time `json:"last_touched_at,omitempty"`
    LastSummonedAt time.Time `json:"last_summoned_at,omitempty"`

    SuccessCriteria   []string              `json:"success_criteria,omitempty"`
    EvidenceChecklist []MissionEvidenceItem `json:"evidence_checklist,omitempty"`
    CurrentPlan       []MissionPlanStep     `json:"current_plan,omitempty"`
    NextAllowedAction string                `json:"next_allowed_action,omitempty"`

    BlockedReason string `json:"blocked_reason,omitempty"`
    WaitingFor    string `json:"waiting_for,omitempty"`

    Authority MissionAuthorityContract `json:"authority"`
    Budget    MissionBudget            `json:"budget"`
    Decay     MissionDecayPolicy       `json:"decay"`

    Tags       []string `json:"tags,omitempty"`
    SourceRefs []string `json:"source_refs,omitempty"`
}
```

Recurrence is explicit and reviewable:

```go
type MissionRecurrence struct {
    Cadence      string    `json:"cadence"` // cron, daily, weekly, on_startup, manual_review
    NextDueAt    time.Time `json:"next_due_at,omitempty"`
    LastRanAt    time.Time `json:"last_ran_at,omitempty"`
    MaxRuns      int       `json:"max_runs,omitempty"`
    RunsUsed     int       `json:"runs_used,omitempty"`
    RequiresTrace bool     `json:"requires_trace"`
}
```

Completion is evidence-bearing, not mood-bearing:

```go
type MissionEvidenceItem struct {
    Claim       string    `json:"claim"`
    Required    bool      `json:"required"`
    EvidenceRef string    `json:"evidence_ref,omitempty"`
    Status      string    `json:"status"` // pending, satisfied, weak, failed, stale
    UpdatedAt   time.Time `json:"updated_at,omitempty"`
}
```

## Authority Contract

Default authority should permit resurfacing, not work.

```go
type MissionAuthorityContract struct {
    CanSelfSummon      bool `json:"can_self_summon"`
    CanSelfContinue    bool `json:"can_self_continue"`
    RequiresUserReview bool `json:"requires_user_review"`

    AllowedTools    []string `json:"allowed_tools,omitempty"`
    AllowedSurfaces []string `json:"allowed_surfaces,omitempty"`
    AllowedPaths    []string `json:"allowed_paths,omitempty"`
    AllowedDomains  []string `json:"allowed_domains,omitempty"`

    CapabilityGrantIDs []string `json:"capability_grant_ids,omitempty"`

    MaxAutonomousSteps int    `json:"max_autonomous_steps,omitempty"`
    ReviewCadence      string `json:"review_cadence,omitempty"`
}
```

Default:

```go
MissionAuthorityContract{
    CanSelfSummon: true,
    CanSelfContinue: false,
    RequiresUserReview: true,
}
```

Any transition that enables `CanSelfContinue`, expands tools, expands surfaces,
adds account/browser/local-device access, or mutates durable child policy must go
through proposal/capability authority.

## Budgets

Budgets are anti-hunger controls, not merely cost controls.

```go
type MissionBudget struct {
    MaxTurns          int `json:"max_turns,omitempty"`
    MaxToolCalls      int `json:"max_tool_calls,omitempty"`
    MaxRuntimeSeconds int `json:"max_runtime_seconds,omitempty"`
    MaxTokens         int `json:"max_tokens,omitempty"`

    TurnsUsed          int `json:"turns_used,omitempty"`
    ToolCallsUsed      int `json:"tool_calls_used,omitempty"`
    RuntimeSecondsUsed int `json:"runtime_seconds_used,omitempty"`
    TokensUsed         int `json:"tokens_used,omitempty"`
}
```

Any self-continuing mission must have at least one visible budget and a review
cadence.

## Decay Policy

Decay prevents spectral drag.

```go
type MissionDecayPolicy struct {
    ReviewAfterDays    int `json:"review_after_days,omitempty"`
    ExpireAfterDays    int `json:"expire_after_days,omitempty"`
    ArchiveAfterDays   int `json:"archive_after_days,omitempty"`
    MaxSummonsPerWeek  int `json:"max_summons_per_week,omitempty"`
}
```

Defaults:

- `candidate`: expire after 30-60 days without touch unless pinned.
- `dormant`: no automatic summon unless relevance is strong.
- `active`: review after 7 days idle.
- `pinned`: never auto-delete, but may become dormant.
- `blocked`: resurface only on new unblocking evidence or review cadence.
- `recurring`: follow explicit recurrence, authority, and budget.
- `completed`: archive after evidence has stabilized.

## Storage

Add `mission_ledger`:

```sql
CREATE TABLE mission_ledger (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    owner TEXT NOT NULL,
    title TEXT NOT NULL,
    objective TEXT NOT NULL,
    origin TEXT NOT NULL,
    status TEXT NOT NULL,
    pinned INTEGER NOT NULL DEFAULT 0,

    recurrence_json TEXT,
    authority_json TEXT NOT NULL,
    budget_json TEXT NOT NULL,
    decay_json TEXT NOT NULL,

    success_criteria_json TEXT,
    evidence_json TEXT,
    current_plan_json TEXT,

    next_allowed_action TEXT,
    blocked_reason TEXT,
    waiting_for TEXT,

    tags_json TEXT,
    source_refs_json TEXT,

    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_touched_at TEXT,
    last_summoned_at TEXT
);
```

Add append-only `mission_events`:

```sql
CREATE TABLE mission_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    mission_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL,
    summary TEXT NOT NULL,
    payload_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE CASCADE
);
```

Add mission-aware handoff/result tables for restart-prone work:

```sql
CREATE TABLE mission_handoffs (
    id TEXT PRIMARY KEY,
    mission_id TEXT,
    operation_id TEXT,
    planned_action TEXT NOT NULL,
    expected_evidence_json TEXT,
    recovery_question TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE SET NULL
);

CREATE TABLE mission_results (
    id TEXT PRIMARY KEY,
    handoff_id TEXT NOT NULL,
    mission_id TEXT,
    operation_id TEXT,
    status TEXT NOT NULL,
    evidence_refs_json TEXT,
    summary TEXT NOT NULL,
    remaining_risk TEXT,
    recorded_at TEXT NOT NULL,
    FOREIGN KEY(handoff_id) REFERENCES mission_handoffs(id) ON DELETE CASCADE,
    FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE SET NULL
);
```

Indexes:

```sql
CREATE INDEX idx_mission_ledger_scope_owner_status
ON mission_ledger(scope, owner, status);

CREATE INDEX idx_mission_ledger_pinned
ON mission_ledger(scope, owner, pinned);

CREATE INDEX idx_mission_ledger_last_touched
ON mission_ledger(last_touched_at);

CREATE INDEX idx_mission_events_mission_id_seq
ON mission_events(mission_id, seq);

CREATE INDEX idx_mission_handoffs_status
ON mission_handoffs(status, created_at);

CREATE INDEX idx_mission_results_handoff
ON mission_results(handoff_id, recorded_at);
```

Initial event vocabulary:

```text
mission.created
mission.proposed
mission.pinned
mission.unpinned
mission.recurrence_enabled
mission.recurrence_disabled
mission.activated
mission.paused
mission.blocked
mission.completed
mission.expired
mission.archived
mission.summoned
mission.snoozed
mission.continuation_requested
mission.continuation_approved
mission.continuation_denied
mission.evidence_updated
mission.budget_updated
mission.authority_changed
mission.handoff_written
mission.result_recorded
```

## Working Objective Inference

Add a `WorkingObjective` operational current-state surface before durable mission
promotion.

```go
type WorkingObjective struct {
    Objective  string    `json:"objective"`
    Source     string    `json:"source"` // inferred, user_explicit, operation_state
    Confidence string    `json:"confidence"`
    CreatedAt  time.Time `json:"created_at"`
    ExpiresAt  time.Time `json:"expires_at,omitempty"`
}
```

Rules:

- Infer for substantive user turns when an operation is not already clearer.
- Use for progress labels, status, completion audit, and restart handoff.
- Never promote to durable mission automatically.
- Propose promotion only when the objective is recurring, multi-turn,
  system-shaping, child-agent-shaping, or explicitly requested.

## User Commands

Prefer `/mission` over `/goal` to avoid creating a second Codex-shaped authority
lane.

Initial commands:

```text
/mission
/mission list
/mission show <id>
/mission create <objective>
/mission pin <id>
/mission unpin <id>
/mission activate <id>
/mission pause <id>
/mission block <id> <reason>
/mission complete <id>
/mission archive <id>
/mission summon
```

Aliases may exist later:

```text
/missions
/objective
```

Bare `/mission` should show current working objective, active missions, pinned
missions, recurring missions, recently summoned missions, and blocked missions.
It should be calm and review-oriented, not nagging.

## Tools

Split reads from authority-bearing writes.

Read actions:

```json
{
  "action": "list|show|summon",
  "mission_id": "..."
}
```

Write/proposal actions:

```json
{
  "action": "propose_candidate|create_candidate|update_evidence|block|archive|event",
  "mission_id": "...",
  "status": "...",
  "objective": "...",
  "authority": {},
  "budget": {},
  "evidence": []
}
```

Allowed model-authored actions:

- create a candidate mission only when the user explicitly asks to track,
  remember, pin, or create a mission
- otherwise propose a candidate mission for review
- update evidence
- mark blocked with reason
- propose completion
- archive when explicitly commanded

Forbidden without proposal/capability gates:

- pin or unpin a mission
- enable recurrence
- enable self-continuation
- widen tools, domains, paths, accounts, or local-device access
- convert a candidate into an approved recurring ritual
- override budget
- mutate durable child policy

## Self-Summon

Self-summon is review-only in the first implementation.

Run it only at bounded moments:

- start of a new user turn
- end of a completed operation
- scheduled heartbeat
- `/mission summon`
- startup recovery
- `/doctor`
- daily review / recurring ritual review windows

Do not run it continuously.

Inputs:

- current user message
- current working objective
- operation state
- plan state
- pending proposals
- active capability grants
- mission status
- pinned flag
- recurrence due state
- blocked state
- review cadence
- time since last touch/summon
- semantic relevance to current context
- whether next action is already authorized

Candidate classes:

```text
review_only
needs_user_input
needs_capability
can_continue_bounded
stale
blocked
```

First implementation only renders review cards and logs `mission.summoned`.

Example card:

```text
Mission surfaced: Lighthouse rename
Reason: Current context mentions Lighthouse/console identity.
Status: candidate
Authority: review only
Next possible action: inspect rename surfaces
Needs approval: yes; this mutates durable identity/config/profile state.
```

Buttons:

```text
Review
Snooze
Archive
Promote
```

Language rule:

- Say: "This mission is relevant again."
- Do not say: "I need to work on this."

## Self-Continuation

Self-continuation comes after self-summon and must reuse existing continuation
machinery.

Flow:

1. Self-summon identifies a `can_continue_bounded` mission.
2. Continuation consensus checks persona intent, governor intent, mission
   authority, operation state, plan state, active grants, budgets, and handoff
   readiness.
3. If eligible, create a continuation event tied to `mission_id`.
4. If not eligible, render review/proposal instead.

Extend continuation state with:

```go
MissionID             string        `json:"mission_id,omitempty"`
MissionStep           string        `json:"mission_step,omitempty"`
AuthorityContractHash string        `json:"authority_contract_hash,omitempty"`
BudgetSnapshot        MissionBudget `json:"budget_snapshot,omitempty"`
HandoffID             string        `json:"handoff_id,omitempty"`
```

Hard rule:

> At most one mission may self-continue per session/context.

If two missions are eligible, choose none and ask for review.

## Restart, Handoff, and Result Artifacts

Mission Ledger must close the current restart/recovery gap: planned
self-restarts need structured handoff/result artifacts so recovery and `/doctor`
can report actual outcomes instead of inferring from chat.

Add mission-aware handoff records when a mission or operation plans a restart,
reinstall, service bounce, child wake, or other interruption-prone action.

Suggested handoff payload:

```go
type MissionHandoff struct {
    MissionID        string    `json:"mission_id,omitempty"`
    OperationID      string    `json:"operation_id,omitempty"`
    HandoffID        string    `json:"handoff_id"`
    PlannedAction    string    `json:"planned_action"`
    ExpectedEvidence []string  `json:"expected_evidence,omitempty"`
    RecoveryQuestion string    `json:"recovery_question"`
    CreatedAt        time.Time `json:"created_at"`
}
```

Suggested result payload:

```go
type MissionResult struct {
    HandoffID     string    `json:"handoff_id"`
    MissionID     string    `json:"mission_id,omitempty"`
    OperationID   string    `json:"operation_id,omitempty"`
    Status        string    `json:"status"` // completed, failed, partial, unknown
    EvidenceRefs  []string  `json:"evidence_refs,omitempty"`
    Summary       string    `json:"summary"`
    RemainingRisk string    `json:"remaining_risk,omitempty"`
    RecordedAt    time.Time `json:"recorded_at"`
}
```

Recovery should prefer `MissionResult`, then TES, then handoff expectations, and
only then compatibility fallback. If evidence is missing, recovery says unknown.

This is required before broad self-continuation. Otherwise the system can appear
to remember outcomes that it only inferred from interrupted chat.

## Proposal and Capability Integration

Pinning and self-continuation are different gates.

Pin mission proposal:

```text
Proposal: Pin mission
Effect: preserve in mission ledger; may resurface for review
Authority: no self-continuation unless separately approved
```

Self-continuation proposal:

```text
Proposal: Allow mission self-continuation
Bounds: surfaces, tools, paths, domains, grants, cadence, budget, review rules
```

Any browser/account/network/local-device/purchase/public-contact action creates a
capability request. Mission state can motivate the request; it cannot replace it.

## `/doctor` Integration

`/doctor` should report mission ledger health:

```text
Mission ledger:
- active: N
- pinned: N
- recurring: N
- blocked: N
- self-continuation enabled: names/ids
- stale candidates: N
- expired grants affecting missions: names/ids
- missing handoff/result artifacts: names/ids
```

Risk checks:

- self-continuation enabled but no active grant
- active grant but expired mission budget
- blocked mission repeatedly summoned without new evidence
- external-account mission without capability request
- restart-prone mission without handoff/result artifacts
- projection claiming completion without evidence refs

## Implementation Sequence

### PR 1 — Mission Ledger Foundation

- Add mission types.
- Add SQLite migrations for `mission_ledger`, `mission_events`,
  `mission_handoffs`, and `mission_results`.
- Add store methods:
  - `CreateMission`
  - `ListMissions`
  - `LoadMission`
  - `UpdateMissionStatus`
  - `SetMissionPinned`
  - `UpdateMissionRecurrence`
  - `AppendMissionEvent`
  - `CreateMissionHandoff`
  - `RecordMissionResult`
- Add mission event tests.
- Add transition validation tests.

No self-summon. No continuation. No autonomy.

### PR 2 — Working Objective Inference

- Add operational `WorkingObjective` state.
- Infer from substantive user turns.
- Wire into operation/progress/completion-audit prompts.
- Prove it does not create durable missions automatically.

### PR 3 — Manual Mission Commands

- Add `/mission`, `/mission list`, `/mission show`, `/mission create`,
  `/mission archive`.
- Add `/mission pin` and `/mission unpin` behind review/confirmation.
- Keep mutation scope narrow.
- Render calm summaries.

### PR 4 — Self-Summon Review-Only

- Add summon scoring.
- Add `mission.summoned` event.
- Add Telegram review cards.
- Add snooze/archive/promote affordances.
- Do not continue work from summons yet.

### PR 5 — Proposal and Capability Integration

- Mission cards can generate proposals.
- Mission cards can generate capability requests.
- Pinning and self-continuation become separate reviewable actions.
- Grants and mission authority hashes are linked.

### PR 6 — Restart Handoff/Result Artifacts

- Add handoff/result records for planned restarts and interruption-prone mission
  work.
- Teach startup recovery and `/doctor` to prefer result artifacts over chat
  inference.
- Block broad self-continuation until this is reliable.

### PR 7 — Bounded Self-Continuation

- Enable exactly one pilot recurring mission first, likely a nightly reflection.
- Require explicit authority contract, active grants, budget, cadence, and
  handoff/result coverage.
- Route through existing continuation state.
- Enforce one self-continuing mission per context.

### PR 8 — `/doctor` and Hygiene

- Add stale candidate detection.
- Add blocked mission review.
- Add expired grant detection.
- Add continuation/budget mismatch detection.
- Add prune/snooze recommendations.

## Test Plan

### Unit Tests

- candidate -> pinned candidate
- pinned candidate -> active pinned
- active -> blocked
- active -> completed
- blocked does not self-continue
- dormant does not self-summon except high relevance
- recurring summons only when due
- completed cannot become active without explicit reopen

### Authority Tests

- inferred working objective does not create durable mission
- candidate mission cannot self-continue
- pinned mission cannot self-continue by default
- self-continuation requires explicit authority contract
- expired capability grant blocks continuation
- external-account need creates capability request, not action
- mutation requires proposal

### Summon Tests

- relevant mission surfaces
- recently summoned mission is suppressed
- blocked mission suppresses without new evidence
- recurring mission surfaces at cadence
- multiple eligible continuations choose none and ask review

### Recovery Tests

- restart preserves mission ledger
- restart does not resume unauthorized mission
- approved continuation survives only if still inside contract
- planned restart writes handoff
- recovery records result
- missing result reports unknown instead of inferred success

### UI Tests

- `/mission` summary renders correctly
- `/mission summon` explains why
- review buttons map to correct actions
- archive/snooze updates ledger
- raw system warnings do not leak into user reply

## Non-Goals

- Do not recreate Codex `/goal` as a separate authority lane.
- Do not let the model infer durable missions for every request.
- Do not let mission memory become pressure to act.
- Do not allow more than one self-continuing mission per context.
- Do not use mission state to bypass capability grants.
- Do not claim completion without evidence.

## First Pilot

Use a nightly reflection as the first self-continuation pilot only after PRs 1-6 land.

Why this pilot:

- already conceptually recurring
- local/private by design
- bounded artifact
- no network needed
- easy morning trace
- low authority footprint

Success criteria:

- it runs only inside approved cadence/budget
- it writes a private artifact
- it writes handoff/result evidence if interrupted
- it leaves a small morning trace
- `/doctor` can explain exactly what happened

## Final Shape

The spine is:

1. Infer the present working objective.
2. Persist durable missions only deliberately.
3. Let missions resurface for review.
4. Require separate authority for action.
5. Cap self-continuation with budgets and decay.
6. Preserve handoff/result evidence across restarts.
7. Route all autonomy through existing governance.

That gives Aphelion continuity without compulsion: direction, not hunger.


### Mission Control surfacing v1

Candidate missions now project into status pending items as `kind=mission` when the mission owner maps to a Telegram chat. This is review-only and does not activate, pin, or self-continue the mission.


### Mission-backed ActionProposal UI v1

`/mission propose <mission_id>` renders a generic `ActionProposal` with
`Deny`, `Ask edit`, and `Approve` buttons. The first approval effect is narrow:
activate the mission for review/planning or record that the proposal needs edits
/ was denied. It does not self-continue, run tools, or grant additional
authority.

### Mission Control proposal cards v1

The system can now propose new Mission Control candidates through the
`mission_ledger` tool action `propose_candidate`. The action queues a review
card with buttons:

- `Add to Mission Control`
- `Ask edit`
- `Park`
- `Reject`

`Add to Mission Control` creates a candidate mission only. It does not activate,
pin, self-continue, run tools, or grant additional authority. The card is a
Mission Control intake gate; execution still requires a later ActionProposal or
ContinuationLease.
