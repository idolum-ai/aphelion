# Package Ownership

![Package map](diagrams/01-package-map.svg)

## Root Package

The root package is the single-binary composition surface.

- Owns CLI command dispatch, install/deploy entrypoints, and process startup.
- Owns Telegram UI glue that adapts transport callbacks into runtime or decision
  APIs.
- May import `runtime` and assemble concrete dependencies.
- Should avoid owning durable domain behavior once a stable lower-level owner
  exists.

Code anchors:

- [`main.go`](../../main.go)
- [`commands.go`](../../commands.go)
- [`maintenance.go`](../../maintenance.go)
- [`telegram_decisions.go`](../../telegram_decisions.go)

## Runtime

`runtime` is the house shell.

- Owns transport ingress/egress, principal/scope/session wiring, and long-lived loops.
- Owns pre-turn shell handoff into species assemblers.
- Owns two execution-family assembly spines: interactive-like (`interactive_like_assembly.go`) and maintenance (`maintenance_turn_assembly.go`).
- Adapts concrete ports into `turn.Machine`.
- Does not own one-turn stage ordering.

Code anchors:

- [`runtime/runtime.go`](../../runtime/runtime.go)
- [`runtime/turn.go`](../../runtime/turn.go)
- [`runtime/interactive_dm_turn.go`](../../runtime/interactive_dm_turn.go)
- [`runtime/interactive_like_assembly.go`](../../runtime/interactive_like_assembly.go)
- [`runtime/maintenance_turn_assembly.go`](../../runtime/maintenance_turn_assembly.go)
- [`runtime/maintenance_turn.go`](../../runtime/maintenance_turn.go)
- [`runtime/durable_group.go`](../../runtime/durable_group.go)

## Turn

`turn` is the one-turn state machine.

- Owns policy by run-kind.
- Owns stage order and commit/delivery orchestration contracts.
- Consumes governor/face/persistence/delivery ports supplied by runtime.

Code anchors:

- [`turn/engine.go`](../../turn/engine.go)
- [`turn/stages.go`](../../turn/stages.go)
- [`turn/policy.go`](../../turn/policy.go)
- [`turn/ports.go`](../../turn/ports.go)

## Pipeline

`pipeline` owns governor/face conversational transforms.

- Brokerage parsing and ratification shaping.
- Floor material extraction and fallback serialization.
- Visible-reply constitution validation and repair contract shaping.
- Render-decision policy helpers.

Code anchors:

- [`pipeline/contracts.go`](../../pipeline/contracts.go)
- [`pipeline/brokerage.go`](../../pipeline/brokerage.go)
- [`pipeline/material.go`](../../pipeline/material.go)
- [`pipeline/fallback.go`](../../pipeline/fallback.go)
- [`pipeline/constitution.go`](../../pipeline/constitution.go)

## Boundary Guards

- [`architecture_import_guard_test.go`](../../architecture_import_guard_test.go) enforces stable import boundaries between composition, runtime, turn, pipeline, transport, storage, and tool packages.
- [`runtime/architecture_invariants_runtime_test.go`](../../runtime/architecture_invariants_runtime_test.go) pins floor/scene and persist-before-deliver behavior.
- [`runtime/interactive_like_assembly_test.go`](../../runtime/interactive_like_assembly_test.go) defends shared interactive-like assembly behavior across DM and durable-group species.
- [`runtime/maintenance_assembly_boundary_runtime_test.go`](../../runtime/maintenance_assembly_boundary_runtime_test.go) defends maintenance-family assembly boundary behavior across heartbeat, cron, and startup recovery species.

## Storage, Transport, and Tools

- `session` owns durable storage records and persistence APIs. It should not
  import orchestration packages.
- `telegram` owns Telegram wire/client behavior. It should not import runtime,
  turn, or pipeline orchestration.
- `tool` owns bounded tool implementations and sandbox integration. It should
  not import runtime, turn, or pipeline orchestration.
- `durableagent` owns child-agent substrate, enrollment, policy, and forensics.
  It may depend on storage contracts, but not on runtime orchestration.
- `githubapp` owns GitHub App key parsing, JWT signing, and installation-token
  exchange. It does not decide runtime authority or inject credentials into
  tools.

Related requirements:

- [`requirements/core.md`](../../requirements/core.md)
- [`requirements/governor.md`](../../requirements/governor.md)
- [`turn-lifecycle.md`](turn-lifecycle.md)
