# State Surfaces

![State surfaces](diagrams/05-state-surfaces.svg)

Aphelion state is intentionally multi-surface.

## Surfaces

- Visible transcript ledger in `session` (`user`/`assistant` scene text).
- Floor sidecars and floor metadata attached per turn.
- Plan state and operation state sidecars.
- Review events and outbound delivery records.
- Turn-run recovery records for startup repair.
- Execution event timeline (`execution_events`) for ingress/turn/tool/delivery truth.

Code anchors:

- [`session/store.go`](../../session/store.go)
- [`runtime/turn_finalize.go`](../../runtime/turn_finalize.go)
- [`runtime/awareness.go`](../../runtime/awareness.go)
- [`turn/awareness.go`](../../turn/awareness.go)
- [`docs/architecture/transparent-execution-sequence.md`](./transparent-execution-sequence.md)

## Classification Matrix

| Surface / Store | Classification | Primary Role |
| --- | --- | --- |
| `session.execution_events` | canonical | Runtime execution truth (append-only event history). |
| `session.messages` + `session.outbound_messages` + `session.review_events` | canonical | User-visible transcript and delivery history. |
| `session.durable_agents` + `session.durable_agent_state` (+ child identity/config files) | canonical | Current mutable identity and policy posture. |
| Parent/child memory files and `rhizome_*` tables | canonical | Retained historical meaning. |
| `/status`, `/debug`, quick-read blocks, progress narration | projection | Operator-facing rendering of current state. |
| `sessions.plan_state_json` + `sessions.operation_state_json` | cache | Fast status snapshots for projection fallback. |
| `turn_runs` | compatibility/cache | Recovery hints and transitional runtime overlays. |
| `pending_decisions` + `sessions.continuation_state_json` | compatibility/cache | Legacy control pending-state fallback. |

## Why This Matters

- Keeps user-visible continuity and machine-audit continuity separate.
- Preserves floor/scene split without losing recovery/review semantics.
- Prevents architecture drift into one hidden “memory blob.”
- Makes `/status`, `/debug`, and progress narration converge on one shared execution timeline instead of independent ad-hoc state machines.

Related requirements:

- [`requirements/sessions.md`](../../requirements/sessions.md)
- [`requirements/operations.md`](../../requirements/operations.md)
- [`requirements/hidden-inputs.md`](../../requirements/hidden-inputs.md)
- [`requirements/reliability.md`](../../requirements/reliability.md)
