# runtime

`runtime` is Aphelion's long-lived process shell.

The top-level package exists to hold live process wiring: transport ingress,
session admission, locks, background loops, concrete dependencies, and the
handoff into turn orchestration. It should stay a shell. Domain subsystems may
live under `runtime` while they are still entangled with live process state, but
pure classification, rendering, projection, adapter-local mechanics, and mature
bounded domains should move behind narrower helper packages or leaf packages.

## Shell Boundary Contract

Top-level `runtime` owns behavior only when that behavior needs direct access to
live process state or live process responsibilities:

- transport ingress and outbound adapter wiring
- principal admission, principal/scope resolution, and session-key construction
- session locking and protection of concurrent turns
- pre-turn shell handoff into species assemblers (`interactive_dm`,
  `maintenance`, durable-group adapters, and durable wake turns)
- construction of concrete governor/face/persistence/delivery/tool ports passed
  into `turn.Machine` or work executors
- background loops and process lifecycle (`heartbeat`, `cron`, startup recovery,
  idle expiry, stale-turn watchdogs, durable wake polling)
- live runtime resources: providers, outbound sender, store, semantic engine,
  transcriber/synthesizer, active-turn cancels, stream controls, model-provider
  caches, recipe state, and tailnet/backend handles
- integration with durable-agent lifecycle and parent review delivery when that
  integration requires store/session/outbound/process coordination

Top-level `runtime` should not become the default owner of domain logic merely
because the domain is important. Prefer a leaf package, helper package, or
smaller internal boundary when code is mostly:

- pure classification or normalization
- deterministic rendering/copy/projection
- command-effect taxonomy
- status/report assembly that can operate from explicit inputs
- adapter-local protocol/client/artifact mechanics
- durable-child or external-channel business semantics that do not require the
  parent process shell directly
- approval/authority helper logic that can be represented as a state-machine
  helper with explicit inputs and tests

Runtime must also not take ownership of one-turn stage order. Stage sequencing
belongs to `turn.Machine` and pipeline contracts. Top-level runtime may assemble
inputs, choose the turn species, hold locks, and record/deliver results, but it
should not duplicate the turn pipeline as hidden control flow.

## Top-Level Growth Rules

When adding a new top-level `runtime/*.go` file or expanding an existing cluster,
ask these questions before coding:

1. **Does this require live process state?** If it needs `Runtime`, locks,
   providers, outbound delivery, store/session mutation, background goroutines,
   or principal/scope resolution, top-level runtime may be appropriate.
2. **Can it be tested without `Runtime`?** If yes, prefer a helper or leaf
   package and keep top-level runtime as an adapter.
3. **Is it creating a domain vocabulary?** If the code introduces its own
   concepts, lifecycle, statuses, or policy taxonomy, record the boundary and
   consider a package before the vocabulary spreads through the shell.
4. **Does it affect authority, privacy, or lifecycle?** Keep the runtime adapter
   explicit and small; move only pure helpers unless the state transition itself
   has dedicated tests.
5. **Will `/status`, `/doctor`, or continuation need to reconcile it later?**
   Define the canonical source and projection surface up front instead of adding
   another ambiguous pending-state source.

A top-level file is acceptable when it is an adapter from live runtime facts into
a bounded domain. It becomes suspect when the file owns the domain's pure rules,
rendering, and lifecycle vocabulary at the same time.

## Extraction Criteria

A subsystem is ready for extraction when most of these are true:

- its functions can accept explicit inputs instead of a full `*Runtime`
- its tests can run without constructing a root runtime or Telegram session
- it has stable concepts that can be named in a package doc
- it has a narrow port back to top-level runtime for store, delivery, locking, or
  provider access
- it does not need to mutate session/operation/authority state implicitly
- behavior-equivalence tests already cover the seam, or can be added before the
  move

A subsystem is **not** ready for extraction when it still owns session locks,
principal/scope admission, transport delivery, background loop timing, direct
provider lifecycle, or authority transitions whose invariants are not yet
covered.

## Live Ownership

`runtime` owns:

- Telegram ingress and outbound adapter wiring
- principal resolution, scope resolution, and session locking
- pre-turn shell handoff into species assemblers (`interactive_dm`,
  `maintenance`, and durable-group adapters)
- background loops (heartbeat, cron, startup recovery, idle expiry)
- durable-agent lifecycle wiring
- assembly of concrete governor/face/persistence/delivery ports for `turn`

## Boundaries

`runtime` is not the main owner of one-turn stage order. Turn sequencing runs
through `turn.Machine`, while conversational mechanics are delegated to
`pipeline`.

Authoritative split for interactive DM turns:

- runtime shell (`turn.go`) owns principal admission, scope resolution, chat
  action loops, session locks, and transport event awareness.
- interactive DM species assembly (`interactive_dm_turn.go`) owns one-turn
  construction: shared interactive-like assembly, coordinator/port wiring, and
  `turn.Machine` invocation.
- `turn` owns stage order once invoked.

Authoritative split for maintenance turns (heartbeat/cron/recovery):

- runtime maintenance loops (`heartbeat.go`, `cron.go`, `recovery.go`) own
  maintenance-specific request synthesis, hidden-input gathering, and post-turn
  fanout behavior.
- maintenance species assembly (`maintenance_turn_assembly.go`) owns one-turn
  construction for the maintenance family: coordinator/port wiring and
  `turn.Machine` invocation.
- `turn` owns stage order once invoked.

## Current Subsystem Map

| Area | Current location | Top-level runtime role | Extraction posture |
| --- | --- | --- | --- |
| Runtime construction and process resources | `runtime.go` | Owns root dependencies, loops, caches, providers, locks, background lifecycle | Keep shell-owned; reduce new fields unless they are live process state |
| Interactive ingress | `turn.go`, `interactive_dm_turn.go`, `interactive_like_assembly.go`, `turn_coordinator_*` | Admits principals, resolves scope/session, locks, assembles ports, invokes `turn.Machine` | Keep shell-owned; one-turn order remains outside runtime |
| Maintenance turns | `maintenance_turn.go`, `maintenance_turn_assembly.go`, `heartbeat.go`, `cron.go`, `recovery.go` | Synthesizes maintenance requests, gathers hidden inputs, invokes maintenance turn species, handles fanout | Keep loop/admission wiring top-level; extract pure summaries/projections when stable |
| Continuation and approval materialization | `continuation*.go`, `operation_phase_gate.go`, `typed_continuation_approval.go` | Coordinates approval offers, leases, operation phase plans, gates, work routing, repair, and user-visible continuation copy | Highest-risk mixed subsystem; extract only narrow pure helpers after invariant coverage |
| Work execution | `work_executor.go`, `codex_work_lane.go`, `runtime/codex` | Selects native/Codex executor and records work evidence around approved continuations | Keep executor selection top-level; keep Codex client/protocol/taxonomy in `runtime/codex` |
| Status and diagnostics projection | `status*.go`, `doctor.go`, `runtime/doctor` | Aggregates router/store/TES/continuation/review/provider/perception signals into operator views | Extract pure projection/render helpers; keep live evidence collection and delivery top-level |
| Durable wake and durable groups | `durable_wake*.go`, `durable_group*.go`, `external_channel*.go` | Polls agents, chooses adapters, holds locks, runs child/parent wake turns, delivers review events | Keep lifecycle wiring top-level; move adapter-local semantics behind ports as they stabilize |
| Leaf helper domains | `runtime/codex`, `runtime/doctor`, `runtime/mission` | Consumed by top-level runtime through explicit helper/runtime boundaries | Good precedent; keep package docs honest about what the shell still owns |
| Media/artifacts/progress | `media_*`, `outbound_media.go`, `operation_artifact_resolver.go`, `progress*`, `tool_progress*` | Bridges runtime evidence, files, progress, and outbound delivery | Extract deterministic rendering/classification; keep fetch/delivery/persistence top-level |
| Authority/autonomy/model slots | `authority_projection*`, `auto_approval*`, `autonomy.go`, `model_slots.go` | Projects and mutates runtime authority/autonomy/model configuration with store-backed state | Keep mutating/admin entrypoints top-level; extract pure projection/rules with explicit inputs |

## Package Map

- `runtime.go`: runtime construction, loops, and process wiring
- `interactive_like_assembly.go`: shared interactive-like turn assembly spine
  used by DM and durable-group turns
- `interactive_dm_turn.go`: interactive DM species assembler boundary and
  one-turn construction
- `maintenance_turn_assembly.go`: maintenance execution-family assembly boundary
  for heartbeat/cron/recovery turns
- `turn.go`, `turn_finalize.go`, `turn_monitor_events.go`,
  `interactive_dm_turn.go`, `maintenance_turn.go`, `durable_wake_turn.go`:
  adapters from runtime facts into `turn`
- `turn_coordinator_common.go`, `turn_coordinator_interactive.go`,
  `turn_coordinator_durable.go`: shared and species-specific coordinator
  adapters
- `durable_wake.go`: pluggable durable wake ingress adapters and shared
  wake-turn substrate
- `external_channel_runtime.go`: shared external-channel lifecycle helpers for
  poll due checks, command attempt/success/failure state, backoff, and adapter
  state containment
- `codex_app_server_channel.go`: top-level wiring for the Codex app-server
  external-channel adapter; helper/client/artifact/work-event mechanics live in
  `runtime/codex/`
- `durable_group.go`, `durable_group_context.go`, `durable_group_review.go`,
  `durable_wake.go`, `durable_wake_loop.go`,
  `durable_wake_scheduled_review.go`: durable-agent channel runtimes and channel
  adapters
- `interactive_dm_turn_runtime_test.go`, `durable_group_runtime_test.go`,
  `durable_wake_runtime_test.go`, `startup_recovery_runtime_test.go`,
  `status_runtime_test.go`: runtime-domain integration suites (by concern)
- `doctor_runtime_test.go`, `doctor_condense_config_test.go`,
  `mission_ask_test.go`, `mission_control_proposal_test.go`: runtime-root
  integration suites for doctor/mission leaf wiring; they stay beside the
  runtime shell because they exercise root `Runtime`, command/wrapper behavior,
  storage, delivery, and Telegram session routing around the leaves.

## Leaf Packages

Runtime leaf packages are bounded helper domains consumed by the top-level
`runtime` shell. They may own local mechanics and pure
formatting/classification logic, but they must not own ingress/session/lifecycle
wiring or broad runtime policy.

- `runtime/codex`: bounded Codex app-server helper package. It owns the
  app-server client, status prompt helpers, artifact manifest helpers,
  work-event projection helpers, and command-effect taxonomy used by the Codex
  work lane. Top-level `runtime` still owns durable-agent wake wiring, executor
  selection, lifecycle loops, and authority state integration.
- `runtime/doctor`: bounded `/doctor` diagnostics package. It owns doctor
  report assembly helpers, evidence sections, Telegram condensation helpers,
  maintainer artifact formatting, and doctor-local adapter contracts. Top-level
  `runtime` still owns command admission, principal resolution, storage/session
  wiring, delivery, and operational issue reporting.
- `runtime/mission`: bounded Mission Ledger helper package. It owns mission
  command rendering, Mission Question classifier/prompt mechanics,
  working-objective helper logic, and mission proposal formatting. Top-level
  `runtime` still owns hidden-input assembly, transport callback integration,
  review-event delivery, and session lifecycle wiring.
