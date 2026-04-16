# Coordinator Boundary Audit

Date: 2026-04-16

Scope reviewed:

- pre-split coordinator monolith in runtime
- `runtime/turn_finalize.go`
- `runtime/turn.go`
- `runtime/durable_group.go`
- `runtime/maintenance_turn.go`
- wrapper surfaces in `face/` and `pipeline/`

## Outcome

### 1. Runtime coordinator density

Decision: keep runtime ownership, but split by concern and tighten naming.

Implemented:

- split coordinator code into:
  - [`runtime/turn_coordinator_common.go`](../../runtime/turn_coordinator_common.go)
  - [`runtime/turn_coordinator_interactive.go`](../../runtime/turn_coordinator_interactive.go)
  - [`runtime/turn_coordinator_durable.go`](../../runtime/turn_coordinator_durable.go)
- removed monolithic coordinator file
- renamed shared internals for ownership clarity:
  - `executeCoordinatorTurnCommon` -> `executeTurnCoordinator`
  - `buildCoordinatorGovernorPrompt` -> `buildTurnCoordinatorGovernorPrompt`
  - `coordinator*` input/output structs -> `turnCoordinator*`

Rationale:

- Common logic remains runtime-owned adapter orchestration because it binds
  runtime facts (`session` compaction, tool monitor, store state loads, prompt
  assembly facts) into `turn` and `pipeline` ports.
- Split files make species adapters (`interactive`, `durable`) auditable without
  changing behavior.

### 2. Transitional wrapper decisions

Decision: remove scaffolding wrappers that duplicate canonical ownership.

Implemented:

- removed face fallback wrapper file
- removed duplicate fallback tests in `face` (canonical tests remain in
  [`pipeline/fallback_test.go`](../../pipeline/fallback_test.go))
- removed pipeline compatibility wrapper
  `ShouldRenderIdolumReply` from [`pipeline/contracts.go`](../../pipeline/contracts.go)

Waiver ledger updated:

- [`ARCHITECTURE_WAIVERS.md`](../../ARCHITECTURE_WAIVERS.md) now marks these as resolved.

### 3. Naming and shape coherence

Decision: prefer names that expose ownership and species context.

Implemented:

- coordinator shared functions/types now carry `turnCoordinator` prefix
  instead of ambiguous `coordinator*Common*` names.
- pipeline render-decision tests now use
  `ShouldRenderInteractiveIdolumReply` naming directly.

## Follow-up watchpoints

- `runtime/turn_finalize.go` and `runtime/durable_group.go` are still dense.
  They are now easier to review incrementally after the coordinator split.
- keep wrapper reintroduction behind explicit waivers.
