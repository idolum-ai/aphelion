# Durable Children

![Durable topology](diagrams/04-durable-topology.svg)

Durable children are bounded child Aphelions. Some may be user-facing personas,
but they remain subordinate to parent governance and policy boundaries.

## Live Shape

- Parent runtime owns durable-agent registry and wake loops.
- Child execution stays scoped by charter, tool scope, and live policy.
- Upward communication is through bounded review artifacts and summaries.
- Telegram relay turns can target a child inline from DM using `agent:<agent_id> ...` and execute in child scope.
- Child wakes can be transport-triggered (`telegram_update`) or scheduler-triggered (`poll`, `push`, `poll_or_push`) depending on the child role.
- Child wake ingress is selected through pluggable runtime adapters; each adapter contributes wake payload synthesis and review finalization semantics.
- The default deployment includes one example scheduled child (`idolum-daily-review`) using the same durable wake substrate: it stages yesterday's transcript into child-local files and starts a plain scheduled check-in chat upward.
- All wake-driven durable work runs through one child-turn substrate (prompt context + governor/face loop + principal-scoped tools), either in-process or isolated (`durable-agent child-run --agent ...`) when bootstrap/isolation is configured.
- Durable lifecycle and policy events are mirrored into TES (`durable.wake.*`, `durable.state.*`, `durable.policy.*`, `durable.parent.acknowledged`) and used to project admin-facing health and latest-apply visibility.
- Operational child/runtime failures are surfaced to admin chat through deduplicated operational alerts.

Code anchors:

- [`durableagent/runtime.go`](../../durableagent/runtime.go)
- [`runtime/durable_group.go`](../../runtime/durable_group.go)
- [`runtime/durable_wake.go`](../../runtime/durable_wake.go)
- [`runtime/durable_wake_loop.go`](../../runtime/durable_wake_loop.go)
- [`runtime/durable_wake_child.go`](../../runtime/durable_wake_child.go)
- [`runtime/durable_wake_turn.go`](../../runtime/durable_wake_turn.go)
- [`runtime/durable_child.go`](../../runtime/durable_child.go)
- [`main_durable_child.go`](../../main_durable_child.go)

## Boundaries

- Child credentials and local storage roots are child-scoped.
- Child work does not directly mutate parent prompt/memory surfaces.
- Durable child runtime reuses `turn` orchestration where lifecycle aligns.
- Child bootstraps must not carry parent Telegram polling credentials or parent principal IDs.
- Parent-child coordination uses bounded runtime channels (local bootstrap/stdout and remote control-plane HTTP), not child Telegram polling.

Related requirements:

- [`requirements/durable-agents.md`](../../requirements/durable-agents.md)
- [`requirements/security.md`](../../requirements/security.md)
- [`requirements/operations.md`](../../requirements/operations.md)
- [`docs/architecture/transparent-execution-sequence.md`](./transparent-execution-sequence.md)
