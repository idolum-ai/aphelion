# Transparent Execution Sequence

The Transparent Execution Sequence (TES) is the append-only runtime event layer
used to keep execution truth auditable across ingress, turn execution, and
delivery.

## Storage

- Table: `execution_events`
- Ordering: monotonic `seq` per `session_id`
- Primary writer API:
  - `AppendExecutionEvent`
  - `AppendExecutionEvents`
  - `ExecutionEventsBySession`
  - `ExecutionEventsByChat`
  - `ExecutionEventsByTypes`

Code anchor: [`session/store.go`](../../session/store.go)

## Retention, Compaction, and Indexing Policy

### Current Implemented Policy

- Retention modes:
  - default (`sessions.tes_retention.enabled = false`): retain TES rows until
    explicit session/runtime deletion.
  - retention GC (`sessions.tes_retention.enabled = true`): prune old TES rows
    by `max_age`, while always preserving at least `min_retained_rows` newest
    rows and deleting at most `max_delete_per_gc` rows per GC pass.
- Export-before-prune:
  - every non-empty TES prune writes an ordered export bundle to
    `sessions.tes_retention.export_dir` before deleting rows.
  - prune fails closed if export writing fails.
- Deletion boundaries:
  - Session-scoped deletion removes all events for that session (`DeleteSession`).
  - Runtime reset removes all events (`ResetRuntime`).
- Compaction: message/session compaction does not rewrite or summarize TES rows.

Code anchors:

- [`maintenance.go`](../../maintenance.go)
- [`config/config.go`](../../config/config.go)
- [`session/store.go`](../../session/store.go) (`DeleteSession`, `ResetRuntime`)
- [`runtime/compaction.go`](../../runtime/compaction.go)

### Indexing Policy

TES write/read behavior currently relies on the following indexes:

- `idx_execution_events_session_seq (session_id, seq)` for per-session ordered
  append/read windows.
- `idx_execution_events_chat_created (chat_id, created_at, id)` for chat timeline
  reads.
- `idx_execution_events_type_created (event_type, created_at, id)` for system-wide
  event-type projections.
- `idx_execution_events_durable_created (durable_agent_id, created_at, id)` for
  durable-agent health and lifecycle projections.

### Query and Projection Policy

- All user-facing projections should request bounded windows (limit + optional
  time boundary), not unbounded full-table scans.
- `/status` and `/debug` projections should prefer TES windows first and use
  compatibility fallback stores only when TES coverage is missing.
- If projection claims conflict with TES evidence, projection text must degrade to
  deterministic, evidence-backed summaries.

### Truth-Class Precedence Rules

- TES is canonical for execution-sequence questions ("what happened, in what
  order, and with what runtime evidence").
- Operational current-state stores remain authoritative for mutable declared
  "now" state where TES is not the canonical question.
- Compatibility fallback surfaces (for example `turn_runs`) are valid only when
  both canonical and operational coverage is unavailable or incomplete for the
  requested claim.
- Compatibility fallback evidence must be source-attributed and must not replace
  canonical TES claims when TES evidence exists.

### Forward Path

- Add optional rollup summaries on top of exported prune bundles for faster
  historical browsing.
- Add operator tooling for listing and replaying retention export bundles.
- Keep enough recent TES history online to preserve debuggability of current and
  recently completed turns without relying on legacy `turn_runs`.

## Event Families

- Ingress
  - `ingress.accepted`
  - `ingress.queued`
  - `ingress.compacted`
  - `ingress.selected`
- Turn lifecycle
  - `turn.started`
  - `turn.stage.changed`
  - `turn.sidecars.captured`
  - `turn.completed`
  - `turn.failed`
  - `turn.interrupted`
- Provider attempts
  - `provider.attempt.started`
  - `provider.attempt.retried`
  - `provider.attempt.failed`
  - `provider.attempt.succeeded`
- Tool lifecycle
  - `tool.started`
  - `tool.succeeded`
  - `tool.failed`
- Delivery
  - `progress.surface`
  - `delivery.progress.sent`
  - `delivery.progress.edited`
  - `delivery.progress.failed`
  - `delivery.final.sent`
  - `delivery.final.failed`
- Continuation control
  - `continuation.offered`
  - `continuation.approved`
  - `continuation.revoked`
  - `continuation.consumed`
  - `continuation.blocked`
- Decision control
  - `decision.opened`
  - `decision.resolved`
  - `decision.expired`
  - `decision.detached`
- Startup recovery
  - `recovery.awake`
  - `recovery.detected`
  - `recovery.issued`
  - `recovery.completed`
  - `recovery.failed`
- Durable runtime lifecycle
  - `durable.wake.started`
  - `durable.wake.completed`
  - `durable.wake.failed`
  - `durable.state.awake`
  - `durable.state.dormant`
  - `durable.policy.applied`
  - `durable.policy.failed`
  - `durable.parent.acknowledged`

Code anchors:

- [`core/execution_events.go`](../../core/execution_events.go)
- [`core/router.go`](../../core/router.go)
- [`runtime/execution_events.go`](../../runtime/execution_events.go)
- [`runtime/progress.go`](../../runtime/progress.go)
- [`runtime/continuation.go`](../../runtime/continuation.go)
- [`runtime/runtime.go`](../../runtime/runtime.go)

## Ingress Sequencing

Telegram ingress now goes through a per-session sequencer before routing. This
ensures chat-local ordering is preserved at dispatch time even when updates are
received in bursts.

Code anchors:

- [`ingress_sequencer.go`](../../ingress_sequencer.go)
- [`main.go`](../../main.go)

## Current Projection Usage

`ChatStatusSnapshot` now derives `TurnPhase` and `TurnPhaseSummary` from TES
`turn.stage.changed` events only.

Operation/plan/hidden-input status sidecars are projected from TES
`turn.sidecars.captured` events when present, with legacy session status reads
as compatibility fallback only.

`SystemStatusSnapshot` is now TES-first for detached control/recovery overlays:

- Decisions: `decision.*` events define current pending decision visibility.
  Legacy `pending_decisions` rows are used only as fallback when no TES state is
  available for a given `decision_id`.
- Continuations: `continuation.*` events define current continuation status per
  chat when present; legacy continuation state remains fallback.
- Startup recovery: a pending startup recovery item is derived from
  `recovery.issued` until a terminal `recovery.completed|recovery.failed` event
  is observed after issuance.

`/debug` now includes explicit TES timeline blocks (`execution_timeline`) for
chat and system views via `RecentExecution` projections sourced from
`execution_events`.

`/status` latest-turn fields now prefer TES turn projections (derived from
`turn.*` + `tool.*` execution events) with `turn_runs` as fallback for missing
coverage windows.

Collapsed `/status` quick-read text is now grounded against rendered status
tokens. If the generated summary contradicts the underlying status payload, it
is replaced with a deterministic snapshot-based summary.

Collapsed `/debug` quick-read text is now grounded against chat execution state:
inconsistent readable summaries are replaced with a deterministic, snapshot-based
summary to avoid "idle/done" drift while turns are failed, blocked, or running.

Continuation events now include proposal/lease identifiers and lease counters when an embedded `ActionProposal` / `ContinuationLease` exists in continuation state.

Continuation approval prompt text is now grounded against TES continuation
events for the same `decision_id` (expected `continuation.offered` while
pending). If evidence is missing or stale, prompt text falls back to the
deterministic continuation prompt template.

Code anchor: [`runtime/status.go`](../../runtime/status.go)

## Scope

TES is now the canonical append-only sequence for ingress/turn/tool/progress
facts. Legacy read models (for example `turn_runs`) still exist as compatibility
fallbacks for operational speed, but are expected to converge further toward
TES-derived projections.
