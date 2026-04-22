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

`ChatStatusSnapshot` now prefers TES stage events for `TurnPhase` and
`TurnPhaseSummary`, with the in-memory phase map as fallback.

Code anchor: [`runtime/status.go`](../../runtime/status.go)

## Scope

TES is now the canonical append-only sequence for ingress/turn/tool/progress
facts. Legacy read models (for example `turn_runs`) still exist for compatibility
and operational speed, but are expected to converge further toward TES-derived
projections.
