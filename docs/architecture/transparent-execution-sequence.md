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
- `/status` and `/health trace` projections should use TES windows for execution truth
  and operational current-state stores for mutable pending state.
- If projection claims conflict with TES evidence, projection text must degrade to
  deterministic, evidence-backed summaries.

### Truth-Class Precedence Rules

- TES is canonical for execution-sequence questions ("what happened, in what
  order, and with what runtime evidence").
- Operational current-state stores remain authoritative for mutable declared
  "now" state where TES is not the canonical question.
- Removed surfaces must be deleted or rejected; they must not source execution
  projections.

### Forward Path

- Add optional rollup summaries on top of exported prune bundles for faster
  archival browsing.
- Add operator tooling for listing and replaying retention export bundles.
- Keep enough recent TES history online to preserve debuggability of current and
  recently completed turns without relying on `turn_runs`.

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
- Model requests
  - `model.request.started`
  - `model.request.succeeded`
  - `model.request.failed`
- Tool lifecycle
  - `tool.started`
  - `tool.succeeded`
  - `tool.failed`
- Tool batches
  - `tool.batch.started`
  - `tool.batch.completed`
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

Telegram poll offsets are gated by a durable ingress ledger:

1. The poller normalizes an eligible work update and writes
   `telegram_ingress_updates(status='accepted')` with the transport update id and
   normalized inbound payload.
2. If the update is consumed by a command/control handler, the accepted row is
   completed before the offset advances.
3. If the update enters the async turn path, it is marked `queued` before the
   offset advances. Startup replay re-runs pending `accepted`/`queued` rows before
   Telegram polling resumes.
4. When a turn begins, the `turn_runs` insert and the ingress transition to
   `running` happen in one SQLite transaction. Turn completion marks the ingress
   row completed, failed, or dropped. Startup recovery marks any still-running
   ingress row tied to an interrupted turn as `interrupted`, and reconciles
   still-running ingress rows tied to already-terminal turn runs.
5. Callback successes and intentionally ignored updates are terminally recorded
   as `completed` or `skipped` before the offset advances.
6. Poison normalization or handler failures are recorded in
   `telegram_ingress_failures` and terminally mark the update as failed, even
   when no accepted work row existed first.
7. If Telegram redelivers an update that already has a terminal ingress row, the
   poller advances the offset without dispatching it again. If an accepted row
   has become `running` or terminal between acceptance and dispatch, command
   routing treats that row as non-dispatchable and leaves the existing ledger
   outcome authoritative.

The invariant is: if `telegram_ingress_offsets.next_update_id` is advanced past
update N, update N is either terminally recorded or recoverably present in the
ingress ledger.

For callback, ignored, and skipped updates, a terminal row can be written without
a prior accepted work row. Those rows use `accepted_at` as the row's ledger-entry
timestamp; only `status`, `completed_at`, and the recorded reason describe the
terminal outcome.

Operator callback work that launches long-running turns, such as `/health
diagnose`, uses a separate callback-work ingress surface. The callback itself is
terminally recorded on the primary poller surface, while the launched work is
accepted/queued on its callback-work surface so startup replay can recover it.
`/reinstall` follows the same accepted-to-queued turn path as ordinary Telegram
work rather than bypassing the ingress ledger.

Code anchors:

- [`ingress_sequencer.go`](../../ingress_sequencer.go)
- [`main.go`](../../main.go)
- [`telegram/poller.go`](../../telegram/poller.go)
- [`session/store_telegram_ingress.go`](../../session/store_telegram_ingress.go)

## Model and Tool Batch Evidence

Provider attempt events describe the configured backend path and failover story.
Model request events describe each concrete request made by a turn loop: attempt
number, history size, tool manifest size, response token usage, model duration,
and whether the request returned tool calls.

Tool lifecycle events remain per-call evidence. Tool batch events describe the
model-emitted batch envelope and execution mode. The runtime may execute a batch
in parallel only when every call is classified as parallel-safe by the concrete
tool registry; otherwise the batch is executed serially in model order. Tool
results are always appended back to the conversation in model order.

Tool batch payloads also carry bounded diagnostic fields:

- `parallel_eligible`: the emitted batch was safe to run in parallel.
- `parallel_safe_count`: number of calls classified safe by the registry.
- `parallel_blocked_reason`: why a batch stayed serial, when known.
- `parallel_missed_opportunity`: true when the model chose a single exploratory
  `exec` command that native file tools could likely have expressed.
- `parallel_missed_reason`: the native-tool affordance that was probably missed.

These fields are evaluation evidence, not authority. They help tune tool
affordances without granting new capabilities or rewriting the model's request.
Prompt/tool-affordance changes that should affect this behavior can be checked
with the opt-in live eval:
`APHELION_LIVE_PARALLEL_TOOL_EVAL=1 go test ./internal/standalonecli -run TestLiveParallelNativeFileToolAffordance -count=1`.

## Current Projection Usage

`ChatStatusSnapshot` now derives `TurnPhase` and `TurnPhaseSummary` from TES
`turn.stage.changed` events only.

Operation/plan/hidden-input status sidecars are projected from TES
`turn.sidecars.captured` events when present, with session status reads as the
operational current-state source for mutable declared work state.

`SystemStatusSnapshot` is now TES-first for detached control/recovery overlays:

- Decisions: `pending_decisions` rows are the actionable operational queue, with
  `decision.*` events providing canonical adjudication evidence.
- Continuations: continuation state rows are the actionable operational state,
  with `continuation.*` events providing canonical offer/approval evidence.
- Startup recovery: a pending startup recovery item is derived from
  `recovery.issued` until a terminal `recovery.completed|recovery.failed` event
  is observed after issuance.

`/health trace` includes explicit TES timeline blocks (`execution_timeline`) for
chat and system views via `RecentExecution` projections sourced from
`execution_events`.

`/status` latest-turn fields are TES turn projections derived from `turn.*` and
`tool.*` execution events.

Collapsed `/status` quick-read text is now grounded against rendered status
tokens. If the generated summary contradicts the underlying status payload, it
is replaced with a deterministic snapshot-based summary.

Collapsed `/health trace` quick-read text is grounded against chat execution state:
inconsistent readable summaries are replaced with a deterministic, snapshot-based
summary to avoid "idle/done" drift while turns are failed, blocked, or running.

Continuation events now include proposal/lease identifiers and lease counters when an embedded `ActionProposal` / `ContinuationLease` exists in continuation state.

Continuation approval prompt text is now grounded against TES continuation
events for the same `decision_id` (expected `continuation.offered` while
pending). If evidence is missing or stale, prompt text falls back to the
deterministic continuation prompt template.

Code anchor: [`runtime/status.go`](../../runtime/status.go)

## Scope

TES is the canonical append-only sequence for ingress/turn/tool/progress facts.
`turn_runs` remains an operational startup recovery/run-bookkeeping table, not a
status/trace fallback source.
