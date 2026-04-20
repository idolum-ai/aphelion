# Durable Children

![Durable topology](diagrams/04-durable-topology.svg)

Durable children are bounded child Aphelions, not alternate public personas.

## Live Shape

- Parent runtime owns durable-agent registry and wake loops.
- Child execution stays scoped by charter, tool scope, and live policy.
- Upward communication is through bounded review artifacts and summaries.
- Telegram relay turns can target a child inline from DM using `agent:<agent_id> ...` and execute in child scope.
- Child wakes can be transport-triggered (`telegram_update`) or scheduler-triggered (`poll`, `push`, `poll_or_push`) depending on the child role.
- Child wake execution runs through isolated child execution (`durable-agent child-run --agent ...`) when a child bootstrap LLM is configured; the current email wake adapter retains inline fallback for unbootstrapped agents.
- Bootstrapped child wakes execute a child-local turn (prompt context + governor/face loop + principal-scoped tools) before emitting bounded upward review artifacts.
- The current `email` adapter (via `gog_cli`) keeps cadence buffering (`synthesis_cadence`) and channel `never_retain` scrubbing before review emission.

Code anchors:

- [`durableagent/runtime.go`](../../durableagent/runtime.go)
- [`runtime/durable_group.go`](../../runtime/durable_group.go)
- [`runtime/durable_email.go`](../../runtime/durable_email.go)
- [`runtime/durable_email_child.go`](../../runtime/durable_email_child.go)
- [`runtime/durable_email_turn.go`](../../runtime/durable_email_turn.go)
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
