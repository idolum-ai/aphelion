# Implementation Plan: Agent-Proposed Tools, Governor-Held Authority

Companion to [agent-proposed-tools-governor-authority.md](agent-proposed-tools-governor-authority.md).

## Goal

Implement a real capability lifecycle with hard boundaries:

1. proposal
2. governor review
3. registration
4. exposure
5. execution

The implementation must make it impossible for an agent to treat drafted capability as registered capability.

## Scope

In scope for v1:

- first-class tool proposal records
- governor review and ratification path
- register/expose lifecycle state
- explicit audit events for each transition
- one narrow governor-owned tool (`search_web`) as the first consumer path

Out of scope for v1:

- broad web browsing or page-fetching
- automatic capability widening without review
- generalized plugin marketplace workflows

## Delivery Model

Use parallel sub-agent workstreams with a single integration owner.

- Integration owner responsibilities: assign write scopes, keep merge order, resolve conflicts, run final verification, and reject shape drift.
- Sub-agent responsibilities: own one bounded file set and deliver tests with the code.
- Consolidation approach (recommended): progressive consolidation after each workstream reaches green tests.
- Consolidation fallback: end-phase consolidation only when two workstreams are deeply coupled.

## Workstream Map (Sub-Agent Assignments)

### WS1: Proposal Data Model and Persistence

Owner: Sub-agent A

Primary files:

- `session/types.go`
- `session/store.go`
- `session/store_test.go`

Tasks:

- Add first-class `ToolProposal` domain types with compact review status (`proposed|approved|rejected`).
- Add optional link from proposal to registered tool identity (`registered_tool_id` or equivalent).
- Add durable store support for proposal rows and lifecycle updates.
- Keep proposal data distinct from generic operation proposal gates.

Merge gate:

- `go test ./session`
- migration path works on fresh and existing sqlite state

### WS2: Governor Tool Surface for Proposal Lifecycle

Owner: Sub-agent B

Primary files:

- `tool/exec.go`
- `tool/update_operation.go`
- `tool/*_test.go` (new and existing)

Tasks:

- Introduce an admin-governed tool surface for proposal submit/show/list/review/register/expose actions.
- Validate compact proposal payload (`tool_name`, `why_now`, `contract` blob).
- Enforce that register/expose actions reject non-approved proposals.

Merge gate:

- `go test ./tool`
- schema validation errors are explicit and operator-readable

### WS3: Decision Broker and Ratification Wiring

Owner: Sub-agent C

Primary files:

- `decision/broker.go`
- `runtime/execution_events.go`
- `runtime/status.go`
- `runtime/*_test.go`

Tasks:

- Wire proposal review into existing pending-decision machinery.
- Ensure only governor-authorized paths can transition `proposed -> approved/rejected`.
- Emit clear transition reasons on timeout, deny, and approval.

Merge gate:

- `go test ./decision ./runtime`
- denied/expired proposals cannot be registered or exposed

### WS4: Register/Exposure Policy and Manifest Shaping

Owner: Sub-agent D

Primary files:

- `tool/manifest.go`
- `tool/exec.go`
- `requirements/tools.md` (contract updates)

Tasks:

- Separate global tool existence from per-principal/per-run exposure.
- Reflect exposure state in the generated manifest.
- Keep default exposure closed for non-target principals.
- Enforce invocation-time authorization semantics: access is decided from current principal state + current exposure policy.
- Ensure stale exposure records alone cannot grant access.

Merge gate:

- `go test ./tool`
- manifest tests prove exposure filtering is real, not prompt-only
- invocation-time access tests prove stale exposure records do not grant access

### WS5: First Tool Implementation (`search_web` via Brave)

Owner: Sub-agent E

Primary files:

- `tool/search_web.go` (new)
- `tool/search_web_test.go` (new)
- `config/config.go`
- `config/config_test.go`

Tasks:

- Implement narrow `search_web(query, limit)`.
- Enforce strict bounds (`limit`, query count cap, no click-through fetch).
- Add config plumbing for Brave credentials with safe failure behavior.
- Preserve canonical-case safety: `search_web` is read-only/external and must not mutate canonical or operational state.

Merge gate:

- `go test ./tool ./config`
- all external calls mocked in tests

### WS6: Auditability, Status, and Docs Alignment

Owner: Sub-agent F

Primary files:

- `core/execution_events.go`
- `runtime/status.go`
- `face/status_render.go`
- `commands_status.go`
- `requirements/operations.md`
- `requirements/tools.md`

Tasks:

- Add event types for proposal/register/expose transitions.
- Surface transitions in status projections with source attribution.
- Align requirements text with the declared lifecycle and authority boundaries.

Merge gate:

- `go test ./core ./runtime ./face`
- status output clearly distinguishes proposed vs registered vs exposed

## Dependency and Merge Order

1. WS1 (foundation)
2. WS2 and WS3 in parallel
3. WS4 after WS2 baseline lands
4. WS5 after WS2 and WS4 land
5. WS6 after WS3 and WS5 land

Recommended merge sequence:

1. WS1
2. WS2
3. WS3
4. WS4
5. WS5
6. WS6

## Consolidation Strategy

### Progressive Consolidation (Preferred)

- Merge each completed workstream immediately after its gate passes.
- Rebase remaining workstreams onto latest mainline after every merge.
- Run targeted package tests on every merge and full test sweep at WS4, WS5, and final.

When to use:

- low to medium coupling
- fast feedback needed
- higher confidence in migration safety

### End-Phase Consolidation (Fallback)

- Keep WS2+WS3 or WS5+WS6 isolated until both are complete, then merge as a bundle.

When to use:

- high cross-file churn causing repeated rebase conflicts
- temporary unstable interfaces between paired workstreams

## Integration Owner Checklist

- Define disjoint file ownership before delegation.
- Require each sub-agent to list changed files and tests run.
- Reject changes that blur authority boundaries.
- Preserve backward-compatible migrations.
- Enforce end-to-end invariant: no execution path for unregistered or unexposed tools.

## Acceptance Criteria (v1)

- Agents can emit a typed tool proposal without pretending capability exists.
- Governor can approve or reject through durable decision state.
- Register/expose are explicit persisted transitions.
- Manifest and runtime execution honor exposure policy.
- Effective access is checked at invocation time; stale exposure records do not grant authority.
- `search_web` works only after registration and explicit exposure.
- Early canonical tool examples remain read-only; mutating tool classes require a later explicit widening phase.
- Status and execution events make each lifecycle step inspectable.

## Verification Sweep

Before final merge, run:

- `go test ./session ./tool ./decision ./runtime ./core ./face ./config`
- `go test ./...`

Manual validation:

1. submit tool proposal
2. reject proposal and confirm registration is blocked
3. approve proposal and register tool
4. verify tool still blocked before exposure
5. expose to target agent/workflow
6. invoke `search_web` successfully
7. confirm status/audit surfaces show full transition chain
