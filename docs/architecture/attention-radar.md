# Attention Radar

_Status: draft architecture direction._

Aphelion's re-entry recommendations should work like an air-traffic control
radar screen: a recommendation is a live track, not a memory that something once
existed.

The motivating bug is simple. A durable operation still said "continue the
gog_cli lifecycle verification" while the live conversation had moved to a
CopilotKit React v2 thread-switch investigation. The recommender treated that
old operation record as current enough to suggest. It had a flight plan, but not
a fresh radar return.

This document defines the recommendation counterpart to
[`identification-game.md`](identification-game.md). The Identification Game
discovers authority shapes. Attention Radar discovers and maintains
recommendable attention tracks.

## Core Metaphor

Air-traffic control is not planning and not execution. It is live situational
awareness over moving objects.

| Radar mechanic | Aphelion recommendation counterpart |
| --- | --- |
| Aircraft track | Recommendable work surface |
| Radar return | Fresh evidence that the surface is still live |
| Flight plan | Durable operation, plan, mission, or thread state |
| Squawk code | Typed candidate identity |
| Altitude, heading, speed | urgency, freshness, authority cost, user fit |
| Stale blip | Outdated recommendation candidate |
| Ghost track | Durable state with no fresh liveness witness |
| Conflict alert | Competing or contradictory candidates |
| Holding pattern | Parked mission or side thread |
| Handoff | Operator selects a track into active work |
| Emergency priority | Blocker, child update, deadline, or unsafe drift |

The design rule is:

> No fresh track, no recommendation.

A durable object can seed a track, but it cannot be the track by itself. The
runtime must ask: "what evidence says this is still live now?"

## Strategic Inversion

Today the re-entry surface answers "what should I suggest next?" by reconstructing
nearby state after a quiet window. It reads the latest turn, current operation,
plan, missions, threads, memory pressure, and evidence hydration, then ranks a
small set of candidates.

That is useful, but it has the same weak shape that authority recovery had before
typed contracts: the producer and consumer meet through nearby context. Nearby
context is not truth.

The inversion is:

- Old shape: quiet window -> reconstruct candidates from current stores -> rank.
- New shape: durable surfaces emit or refresh attention tracks -> projection
  queries live tracks -> rank only tracks with current witnesses.

The recommendation layer should become a projection over an attention frontier,
not a fresh inference pass over every nearby surface.

## Truth Class

Attention tracks are canonical for recommendation liveness:

- What recommendable surface exists?
- Which source object owns the track?
- Which evidence last refreshed it?
- Is the track live, stale, superseded, ignored, selected, or expired?
- Which operator or runtime event changed that state?

Recommendation cards are projections of tracks. They are not the source of
truth. A model may rank and phrase tracks, but it must not invent tracks or
promote a ghost track to live.

This mirrors the authority discovery split:

| Authority discovery | Recommendation discovery |
| --- | --- |
| Authority shape | Attention track |
| Identification ledger entry | Attention frontier entry |
| Observation | Track return / provenance event |
| Live authority witness | Live attention witness |
| Menu token | Recommendation token |
| Next grant | Next track / inspect frontier |

## Attention Track Model

Illustrative shape:

```go
type AttentionTrack struct {
    TrackID      string
    SessionID    string
    ScopeKind    string
    ScopeID      string

    SourceKind string // operation | plan | mission | thread | child_update |
                      // next_action | memory_pressure | goal_projection
    SourceRef  string
    TrackHash  string // stable over the source + attention archetype

    Status string // live | holding | selected | ignored | stale |
                  // superseded | expired | invalidated

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
                    // user_fit | freshness | supersession
    Value       string
    EvidenceRef string

    ActorKind      string
    ActorPrincipal string
    ActorAction    string

    ObservedAt time.Time
    ExpiresAt  time.Time
}
```

The entry is the stable object. Returns are the provenance history. A track can
cool or fade without being deleted. Repeated returns can reacquire a stale track.
Ignored tracks dampen. Selected tracks hand off into active work.

## Track Sources

The first source set should be small:

| Source | Track condition |
| --- | --- |
| Operation | A current operation agrees with the working objective, or the operator explicitly selected it |
| Plan | A current plan step is unfinished and still relevant to the working objective |
| Mission | A mission is active, blocked, pinned, or recently summoned |
| Thread | A side thread is open and has recent activity or explicit recall value |
| Child update | A durable child task reported a blocker, update, or result needing operator attention |
| Next action | A typed `next_action_record` remains unresolved and actionable |
| Memory pressure | A recurring interior signal has enough intensity and freshness |
| Goal projection | Predictive interception says a goal changed, stalled, or needs a boundary decision |

Each source has a liveness witness. A witness is not just "the row exists." It is
the source-specific proof that the row still points at a current situation.

Examples:

- Operation witness: operation objective overlaps current working objective, or
  the latest operator message explicitly selects that operation.
- Next-action witness: unresolved row, matching session/scope, non-terminal
  operation handoff, and no superseding row for the same subject.
- Thread witness: open thread plus last activity or explicit operator recall.
- Child-update witness: child task result still has an unresolved task-packet
  next action or a recent review event.

## Menu And Radar

The existing re-entry card is a presentation surface. The new shape should be:

```text
attention tracks
  join current liveness witnesses
  join operator history and dampening state
  join authority-discovery menu when execution would need approval
  -> recommendation radar projection
```

The radar projection is a current-state query. It should not rewrite the tracks
it renders, except to record explicit presentation events such as shown, ignored,
selected, or expired-unreviewed.

The model can help rank tracks, but the payload should contain only typed
candidate IDs and non-sensitive metadata. The model must not be able to create a
new source, label, or live witness.

## Selection

Selecting a recommendation should compile a typed work token.

Examples:

- `resume_operation(track_id, operation_id)`
- `inspect_next_action(track_id, next_action_record_id)`
- `review_child_update(track_id, task_packet_id)`
- `reopen_thread(track_id, chat_id, thread_id)`
- `inspect_memory_pressure(track_id, signal_id)`
- `continue_goal_pursuit(track_id, goal_id)`

The selected token becomes the active work surface, or the track is marked stale
if the source can no longer satisfy the selection. Selection should not merely
send a prompt that asks the model to infer what the button meant.

## Staleness And Ghost Tracks

A ghost track is a durable source row with no fresh liveness witness.

Ghost tracks are not deleted. They can remain inspectable evidence. They cannot
rank as "now" unless reacquired by a fresh return.

Rules:

- A stale operation state may be shown in details, but not recommended as live.
- A selected track should supersede conflicting tracks or force a conflict card.
- A recommendation ignored repeatedly should dampen by track hash.
- A recommendation that expires without review should record operator absence.
- A stale recommendation callback should not bind to a newer track; it should
  render or link to the current radar view.

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

## Testing

The interesting tests are not "does a card render." They are track-faithfulness
tests.

Required journey tests:

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

Eval metrics:

- stale recommendation rate;
- selected recommendation completion rate;
- repeated ignored-track rate;
- ghost-track suppression rate;
- time from live blocker to surfaced recommendation;
- operator touches per useful resumed work item.

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

## Design Rule

Recommendation text is presentation. Track liveness is typed state.

A durable row can seed a recommendation only after a source-specific live
witness reacquires it. Otherwise it is a ghost track: inspectable, citeable, and
recoverable, but not a current next step.
