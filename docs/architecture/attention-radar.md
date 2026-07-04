# Attention Radar

_Status: draft architecture direction._

**Live-track recommendations for Aphelion**

Long-running Aphelion work can fail in a boring way: the system remembers an
old piece of work and recommends it as if it were still alive.

That is not a ranking bug. It is a liveness bug.

The motivating incident is small enough to keep the whole design honest. A
durable operation still said "continue the gog_cli lifecycle verification" while
the live conversation had moved to a CopilotKit React v2 thread-switch
investigation. The recommender treated the old operation record as current
enough to suggest. It had a flight plan, but not a fresh radar return.

This document defines the recommendation counterpart to
[`identification-game.md`](identification-game.md). The Identification Game says
that authority affordances are discovered from safe collisions and projected
from a ledger. Attention Radar says that recommendations are live tracks,
projected from fresh returns, not memories of durable rows.

The design reduces to one rule:

> No fresh track, no recommendation.

Durable state can seed a track. It cannot be the track by itself.

## Two Screens, One Boundary

Air-traffic control gives Aphelion the right recommendation mechanic. A radar
screen is not a plan, not a mission, and not an executor. It is a live
situational awareness surface over moving objects.

The controller may know an aircraft's filed route, but it does not recommend
action from the flight plan alone. It waits for returns. It correlates returns
into tracks. It lets tracks coast briefly when a return is missing. It drops or
labels ghost tracks when the signal no longer supports the object. It raises
conflict alerts when two live tracks cannot both own the same lane.

Aphelion needs exactly that mechanic for recommendations.

| Radar mechanic | Aphelion counterpart | Invariant |
| --- | --- | --- |
| Sweep | Periodic recommendation scan | Ranking only considers candidates reacquired by the current sweep |
| Radar return | Fresh typed evidence for liveness | A durable row is not enough |
| Track initiation | First supported recommendable surface | Track ID is stable over source + attention archetype |
| Track correlation | Repeated returns joined to one track | Repeated hints become cumulative knowledge, not new cards |
| Coasting | Short grace after missed return | Staleness is explicit and bounded |
| Reacquisition | Fresh return after stale period | Old work can become current again only with new evidence |
| Ghost track | Durable state without live witness | Inspectable evidence, not a current next step |
| Conflict alert | Competing live tracks | Ask or show conflict; do not hide the fork in ranking |
| Holding pattern | Parked or dampened track | Remember without interrupting |
| Handoff | Operator selects a track | Selection compiles a typed work token |
| Squawk code | Typed candidate identity | Button callbacks bind exact track IDs |
| Emergency priority | Blocker, unsafe drift, deadline, child update | Priority is a typed property, not urgent prose |

The analogy matters because it prevents a subtle category error. A flight plan
is durable. A track is current. They are allowed to disagree. The radar screen is
valuable precisely because it can say: "that durable plan exists, but I do not
currently see the aircraft."

## The Inversion

Today the re-entry surface asks a question after a quiet window:

```text
current stores + latest turn + memory pressure
  -> reconstruct candidate set
  -> rank
  -> recommend
```

That is useful, but it repeats the old recovery mistake. The consumer tries to
rebuild what must be true from nearby state. Nearby state is not truth.

The radar inversion is:

```text
durable surfaces emit returns
  -> returns maintain attention tracks
  -> projection joins live tracks with operator history and authority state
  -> rank only current tracks
```

The producer of liveness writes the liveness fact. The recommendation card
projects it. If no source can produce a current return, the old row becomes a
ghost track: visible in details, unavailable as the main next step.

## Truth Class

Attention tracks are canonical for recommendation liveness:

- What recommendable surface exists?
- Which source object owns it?
- Which evidence last refreshed it?
- Which properties are known: urgency, freshness, authority cost, user fit,
  conflict, dampening?
- Is the track live, coasting, holding, selected, ignored, stale, superseded,
  expired, or invalidated?
- Which operator or runtime event changed that state?

Recommendation cards are projections of tracks. They are not the source of
truth. A model may rank and phrase tracks; it must not invent tracks, liveness,
selection semantics, or callback identity.

This mirrors the authority discovery split:

| Authority discovery | Recommendation discovery |
| --- | --- |
| Authority shape | Attention track |
| Identification ledger entry | Track entry |
| Observation | Radar return / provenance event |
| Live authority witness | Live attention witness |
| Menu token | Recommendation token |
| Next grant | Next track / radar sweep |
| Tail shedding | Ghost suppression and expiry |

## Track Model

Illustrative shape:

```go
type AttentionTrack struct {
    TrackID   string
    SessionID string

    ScopeKind string // session | chat | mission | operation | thread
    ScopeID   string

    SourceKind string // operation | plan | mission | thread | child_update |
                      // next_action | memory_pressure | goal_projection
    SourceRef  string
    TrackHash  string // stable over source + attention archetype

    Status string // live | coasting | holding | selected | ignored |
                  // stale | superseded | expired | invalidated

    FreshnessClass string // fresh | cooling | stale | unknown
    PriorityClass  string // emergency | now | soon | later
    AuthorityCost  string // none | read_only | approval_needed | high

    LastReturnAt time.Time
    ExpiresAt    time.Time
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type AttentionTrackReturn struct {
    TrackID string

    Method string // source_event | static | operator | child_report |
                  // evidence_hydration | recommendation_click | absence
    Property string // liveness | urgency | conflict | authority_cost |
                    // user_fit | freshness | supersession | dampening
    Value       string
    EvidenceRef string

    ActorKind      string
    ActorPrincipal string
    ActorAction    string

    ObservedAt time.Time
    ExpiresAt  time.Time
}
```

The track is the durable identity. Returns are the provenance history. Track
status is not a model summary; it is the current projection of returns,
freshness policy, operator actions, and supersession rules.

Graduated identification applies here too. A first return might identify only
"this is operation-shaped." Later returns can refine authority cost, urgency,
conflict, and user fit. A recommendation should not need one magical perfect
label before it can be useful; it should carry a partial label honestly.

## Track Sources

The first source set should be small and source-owned:

| Source | Live return condition |
| --- | --- |
| Operation | Current operation agrees with the working objective, or the operator explicitly selected it |
| Plan | Current plan step is unfinished and still relevant to the working objective |
| Mission | Mission is active, blocked, pinned, or recently summoned |
| Thread | Side thread is open and has recent activity or explicit recall value |
| Child update | Child task reported an unresolved blocker, update, or result needing attention |
| Next action | Typed `next_action_record` remains unresolved and actionable |
| Memory pressure | Recurring interior signal has enough intensity, freshness, and evidence |
| Goal projection | Predictive interception says a goal stalled, moved, satisfied criteria, or needs a boundary decision |

Each source owns its liveness witness. A witness is not merely "the row exists."
It is the source-specific proof that the row still points at a current
situation.

Examples:

- Operation witness: operation objective overlaps current working objective, or
  the latest operator message explicitly selects that operation.
- Next-action witness: unresolved row, matching session/scope, non-terminal
  handoff, and no superseding row for the same subject.
- Thread witness: open thread plus last activity or explicit operator recall.
- Child-update witness: child task result still has an unresolved task-packet
  next action or a recent review event.
- Goal-projection witness: current goal version, current evaluator version,
  unresolved criterion or boundary decision, and no operator acceptance.

## Sweep And Projection

The existing re-entry card is a presentation surface. The radar shape is:

```text
track entries
  join current liveness witnesses
  join recent returns
  join operator history and dampening state
  join authority-discovery menu when selection would need approval
  -> radar projection
```

The sweep should be deterministic where possible. Model ranking is allowed only
after the candidate set is typed. The model receives track IDs and bounded
metadata; it returns an ordering or an abstention. It cannot create a track,
reacquire a ghost, or decide that a selection is executable.

The projection should record presentation events:

- shown;
- selected;
- ignored;
- parked;
- expired-unreviewed;
- conflict-rendered;
- superseded;
- stale-callback.

Those events are returns too. The operator's inaction and attention are part of
the signal. Silence that expires a card is an actor-stamped return, not missing
history.

## Selection And Handoff

Selecting a recommendation compiles a typed work token:

- `resume_operation(track_id, operation_id)`
- `inspect_next_action(track_id, next_action_record_id)`
- `review_child_update(track_id, task_packet_id)`
- `reopen_thread(track_id, chat_id, thread_id)`
- `inspect_memory_pressure(track_id, signal_id)`
- `continue_goal_pursuit(track_id, goal_id)`

The token becomes the active work surface, or the track is marked stale if the
source can no longer satisfy selection. Selection should never merely send a
prompt that asks the model to infer what the button meant.

Callback identity is exact. A stale recommendation callback must not bind to a
newer track silently. It should render the current radar view or a stale-card
message that points at the new track.

## Staleness, Coasting, And Ghosts

A track can be current, cooling, stale, or a ghost.

- **Live:** current return plus source witness.
- **Coasting:** no new return yet, but still inside a short source-specific
  grace window.
- **Holding:** intentionally parked or dampened; inspectable, not interrupting.
- **Stale:** freshness window passed or superseded by a stronger source.
- **Ghost:** durable source row still exists, but no live witness supports
  recommendation.

Ghost tracks are not deleted. They remain inspectable evidence and can be
reacquired by a fresh return. They cannot rank as "now."

Rules:

- A stale operation state may be shown in details, but not recommended as live.
- A selected track should supersede conflicting tracks or force a conflict card.
- Repeated ignores dampen by track hash without suppressing unrelated tracks.
- Expiry without review records operator absence.
- Coasting windows must be source-specific and short; indefinite coasting is a
  ghost with nicer wording.

## Where Aphelion Is Already Strong

The current tree already has pieces that should be leveraged:

- `runtime/reentry_recommendation.go` already sweeps, ranks, delivers cards,
  records selections, and has deterministic fallback behavior.
- `session/reentry_recommendation_types.go` and
  `session/store_reentry_recommendations.go` already model recommendation
  records and transitions.
- `runtime/recovery_candidate_arbitration.go` already contains stale-vs-current
  suppression logic for recoverable work.
- `next_action_records` already represent typed unresolved work.
- The identification ledger from `identification-game.md` provides the local
  pattern for entry plus observation provenance.
- `/frontier` provides a precedent for a compact operator inspection panel.
- Supersession and stale-callback handling already exist in nearby approval and
  child-wake surfaces.

The fix is not a new platform. It is a canonical liveness surface that the
existing recommendation machinery can project.

## Where Aphelion Is Weak

The current re-entry surface does not yet have a durable liveness spine.

Weaknesses this design closes:

- **Flight-plan confusion:** an operation, mission, or thread can be durable but
  no longer live.
- **Producer/consumer drift:** the recommendation sweep reconstructs liveness
  from neighboring state instead of consuming source-owned returns.
- **Stale-card ambiguity:** old callbacks must not silently select newer work.
- **Silence loss:** ignored or expired recommendations should shape future
  recommendation pressure.
- **No cumulative track memory:** repeated stale suggestions are not yet a
  durable signal with dampening and reacquisition semantics.
- **No optimization target:** recommendation quality is not scored as ghost
  suppression, reacquisition speed, useful selection rate, and interruption cost.

## Presentation

The operator should see compact track cards:

```text
Attention radar

1. Continue React v2 thread-switch probe
   live: latest operator turn + dirty test file
   cost: local read/test; approval if write/push needed

2. Review daily child update
   live: unresolved child task update
   cost: read-only review

3. Inspect memory pressure
   live: recurring semantic signal
   cost: read-only
```

Each row should answer:

- why this is live now;
- what source owns it;
- what selecting it will compile;
- whether authority will be needed after selection.

The details surface should link to the underlying track, returns, evidence refs,
and any related authority-discovery menu entry.

## Relationship To Predictive Interception

Attention Radar answers:

> What live track deserves attention?

Predictive Interception answers:

> Given a selected track and a goal, where should the plan aim next?

The layers should not collapse.

Recommendations can suggest a goal track, but they should not execute the
controller. Predictive interception may emit tracks when a goal stalls,
regresses, reaches criteria satisfaction, or needs a boundary decision. The
radar projects those tracks alongside ordinary operations, missions, threads,
and child updates.

## Testing As An Optimization Problem

The interesting tests are not "does a card render." They ask whether the radar
keeps the operator's attention on live tracks at low cost.

Hard invariants:

- A ghost track is never rendered as the primary current recommendation.
- A stale callback never binds to a different live track.
- Model ranking never creates, reacquires, or selects a track.
- Selecting a track compiles a typed token or fails closed.
- Operator silence is recorded when a recommendation expires unreviewed.

Journey tests:

- **Ghost operation:** an operation remains active but the working objective has
  moved; the old operation is not recommended without explicit selection.
- **Reacquired operation:** the user says "go back to X"; the old operation gets
  a fresh return and can be recommended.
- **Next-action freshness:** unresolved next action ranks while current; a
  terminal or superseded next action fades.
- **Child update:** a child task update creates a live track; resolving or
  superseding the task cools it.
- **Ignore dampening:** repeated ignores suppress the same track hash without
  suppressing unrelated tracks.
- **Stale callback:** pressing an old recommendation callback never binds to a
  newer track silently.
- **Conflict alert:** two tracks compete for the active work lane; the card asks
  or offers a choice instead of ranking both as confident next steps.

Optimization metrics:

- stale recommendation rate;
- ghost-track suppression rate;
- live-blocker miss rate;
- time from live blocker to surfaced recommendation;
- reacquisition latency after explicit recall;
- selected recommendation completion rate;
- repeated ignored-track rate;
- operator touches per useful resumed work item;
- conflict-resolution interruption cost.

The target is not maximal recommendation volume. The target is a Pareto frontier
between useful live resurfacing and low operator interruption. A system that
never recommends is quiet but useless. A system that recommends every durable
row is loud and false. The radar is good when it makes live tracks visible while
keeping ghosts out of the main lane.

## File-Level Direction

Current surfaces to leverage:

- `runtime/reentry_recommendation.go`: existing sweep, card delivery, ranking,
  dampening, and callback flow.
- `session/reentry_recommendation_types.go`: existing recommendation record and
  candidate types.
- `session/store_reentry_recommendations.go`: current persistence and
  transition model.
- `runtime/recovery_candidate_arbitration.go`: existing stale-vs-working-objective
  suppression seed.
- `session/identification_ledger_*`: model for entry + observation provenance.
- `runtime/authority_discovery_menu.go`: model for projection over typed
  discovery state and live witnesses.
- `core/status.go` and `/frontier`: model for typed operator inspection panels.

Likely additions:

- `session/types_attention_track.go`
- `session/store_attention_tracks.go`
- `runtime/attention_radar.go`
- `runtime/attention_radar_projection.go`
- `runtime/attention_radar_test.go`
- `/radar` or a `/status` subsection for current tracks

## Implementation Arc

The PR sequence should follow the same discipline as PR #283:

1. **Canonical surface first:** define track and return types, liveness
   witnesses, and ghost-track semantics.
2. **Tests before schema pressure:** write journey tests for ghost operation,
   reacquisition, stale callback, child update, and ignore dampening.
3. **One durable ledger:** add attention tracks only when the signatures are
   stable enough to avoid schema churn.
4. **Projection next:** make `/status` or `/radar` render tracks from the
   canonical ledger.
5. **Runtime consumers last:** move re-entry recommendations to consume radar
   projections instead of reconstructing liveness from nearby state.

The first implementation slice should be honest if it stops at the substrate:
typed tracks, returns, liveness witnesses, ghost suppression, and a compact
inspection surface. Predictive Interception can consume tracks later.

## Design Rule

Recommendation text is presentation. Track liveness is typed state.

A durable row can seed a recommendation only after a source-specific live
witness reacquires it. Otherwise it is a ghost track: inspectable, citeable, and
recoverable, but not a current next step.
