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

## Event Families

- Ingress
  - `ingress.accepted`
  - `ingress.queued`
  - `ingress.compacted`
  - `ingress.selected`
- Turn lifecycle
  - `turn.started`
  - `turn.stage.changed`
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

Continuation approval prompt text is now grounded against TES continuation
events for the same `decision_id` (expected `continuation.offered` while
pending). If evidence is missing or stale, prompt text falls back to the
deterministic continuation prompt template.

Code anchor: [`runtime/status.go`](../../runtime/status.go)

## Scope

TES is now the canonical append-only sequence for ingress/turn/tool/progress
facts. Legacy read models (for example `turn_runs`) still exist for compatibility
and operational speed, but are expected to converge further toward TES-derived
projections.
