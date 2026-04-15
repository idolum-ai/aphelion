# Turn / Pipeline Refactor Plan

This document describes how to reorganize the repo so the code matches the
system's lived architecture more closely:

- `runtime` as the long-lived house shell
- `turn` as the state machine for one scoped turn
- `pipeline` as the governor/face mechanics inside that turn
- `session` as the durable ledger and sidecar substrate
- `durableagent` as the bounded-child governance layer

The goal is not to rewrite the system around new names. The goal is to move the
live code toward the architecture it already describes.

## Why This Refactor Exists

Today, the core concepts are already real:

- bicameral governor/face execution
- governor-owned floor and face-authored scene
- hidden-input awareness
- sidecar durable state beyond transcript
- heartbeat, cron, and recovery as long-lived house rhythms
- bounded durable children with upward review
- multiple delivery modes from the same upstream turn

But the live code still concentrates too much of this inside `runtime`.

That makes several things harder than they should be:

- reasoning about who owns a turn stage
- reusing the same turn machinery across interactive, durable, and maintenance work
- testing the turn lifecycle without bringing the full process shell with it
- evolving brokerage, floor, scene, and fallback without growing `runtime` further

## Guiding Rules

1. Keep one public persona.
   Idolum remains the public face. Aphelion remains the judgment layer. Do not
   create separate user-facing stacks or alternate visible selves.

2. Refactor by ownership, not by theme words.
   Do not introduce top-level `idolum/` or `aphelion/` packages. The useful code
   boundaries are turn orchestration, pipeline mechanics, persistence, runtime
   shell, and durable-agent governance.

3. Preserve floor/scene separation everywhere.
   The governor-owned floor is not an implementation accident. The visible scene
   must continue to be staged from a bounded material artifact.

4. Preserve commit discipline.
   Persisted transcript, floor sidecars, outbound delivery, review events, and
   turn-run tracking must keep explicit ordering.

5. Extract behavior behind tests before moving ownership.
   No large blind moves. Promote existing runtime behavior into stable contracts,
   then move code behind those contracts.

6. Share machinery where the lifecycle is actually the same.
   Interactive DMs, durable Telegram children, heartbeat, cron, and startup
   recovery should share turn machinery when their stage order is the same, and
   stay separate where their authority or delivery semantics are genuinely different.

7. Treat turn species as a contract vocabulary, not a premature unification promise.
   "Interactive", "durable child", "heartbeat", "cron", and "recovery" are useful
   classifications. They do not imply that one elegant engine should absorb every
   semantic difference immediately.

8. Keep `pipeline` narrow and ruthless.
   `pipeline` may own conversational transformations across governor and face
   boundaries. It must not quietly absorb turn policy, commit ordering, transport
   semantics, or provider shell ownership under a cleaner package name.

9. Treat awareness assembly as an ownership problem, not just a data-shaping task.
   Hidden inputs, plan state, operation state, brokerage state, and delivery state
   should be assembled by the layer that materially owns the meaning of that state.

10. Treat commit semantics as constitutional machinery.
    Persist, deliver, record-delivered, stream finalization, review-event enqueue,
    and failure handling are core behavioral guarantees, not incidental plumbing.

11. Treat architecture gates as explicit, testable constraints.
   - Turn species are classification labels for policy, delivery, and authority
     differences; they are not a promise of one universal turn engine.
   - `pipeline` should remain narrow to governor/face transformations and not grow
     into runtime-like transport or provider ownership.
   - Awareness fields should be assembled only where ownership is clear.
   - Commit and persistence semantics are first-class contracts and should be
     extracted only after they have explicit tests.

## Status Language

This plan uses three status labels for architecture claims:

- `live truth`: already the real production owner
- `migration seam`: partially real, but still split or duplicated across layers
- `northbound target`: intended final owner, not yet the live production owner

## Progress Snapshot

As of this commit series, the refactor has advanced on one bounded slice:

- `pipeline`: added contract-boundary extraction for turn policy, turn execution shape, and floor-material parsing/formatting.
- `pipeline`: added pure helpers and tests for material floor parsing (`ParseMaterialPacket`, `BuildFloorFromGovernor`).
- `runtime`: began ownership handoff by delegating prepare/material/face-policy/awareness execution shaping to `pipeline` contracts.
- `requirements`: encoded additional cautionary rules (species as explicit policy vocabulary, pipeline scope boundaries, and test-first commitment extraction).
- `tests`: `go test ./...` remains green after this slice.
- `pipeline`: moved face render decision from a passive wrapper into explicit behavior for `ShouldRenderIdolumReply` (command guard, empty-input guard, and no-response/tool-context handling), with tests.
- `pipeline`: introduced `RenderDecisionInput` and `ShouldRenderInteractiveIdolumReply` to keep render-decision inputs explicit and avoid argument drift; retained `ShouldRenderIdolumReply` as compatibility delegation.
- `runtime`: removed passive face-policy helper implementations that duplicated pipeline logic.
- `runtime`: removed the runtime `face_policy` facade entirely and inlined canonical pipeline calls (`DecideInteractiveFacePolicy`, `ShouldRenderInteractiveIdolumReply`, `ParseExecutionContract`) at call sites.
- `runtime`: removed material shim layer (`runtime/material.go`) and moved equivalent coverage into `pipeline` tests (`pipeline/material_test.go`), leaving runtime with direct pipeline calls for floor/material conversion.
- `pipeline`: added brokerage parsing ownership (`ParseExecutionContract`, `ParseBrokerageRatification`) with tests for execution contract extraction and ratification contract validation. `runtime` now consumes these as parsing inputs while keeping turn orchestration ownership.
- `runtime`: removed runtime mirrors of pipeline contracts and now consumes canonical pipeline contracts (`ExecutionContract`, `FacePolicy`, `TurnPrepareContract`, `TurnExecutionContract`) directly.
- `pipeline`: removed in-package `PreparedTurn` / `GovernorExecution` aliases to keep one canonical contract name per concept and prevent mirrored type drift from the source.
- `runtime`: added commit-semantics test (`TestHandleInboundPersistsWhenSendFails`) to pin down persisted transcript state when outbound delivery fails.
- `runtime`: removed the remaining mirrored contract aliases for execution/awareness/policy/prep phases and now uses `pipeline` contracts directly at call sites.

## Target Package Ownership

### `runtime`

Status: `live truth` moving toward a narrower shell.

`runtime` should own:

- process lifecycle
- channel ingress and outbound adapters
- router integration
- session locking
- startup recovery scheduling
- idle expiry, heartbeat, cron, and durable-agent wake loops
- principal/scope resolution
- assembly of concrete ports passed into a turn engine

`runtime` should stop owning the detailed order of one ordinary turn.

### `turn`

Status: `migration seam` today, `northbound target` for production turn orchestration.

`turn` should own:

- turn species and turn policy
- stage ordering for one scoped turn
- orchestration from inbound request to committed result
- explicit ports for governor execution, face rendering, persistence, and delivery
- commit-order rules
- reusable turn execution across interactive and bounded-child contexts

`turn` should not be forced to erase meaningful differences between turn species.
The immediate goal is shared orchestration where the lifecycle is genuinely the
same, not a single universal engine at any cost.

### `pipeline`

Status: `northbound target`.

`pipeline` should own:

- brokerage proposal and ratification mechanics
- governor result to floor extraction
- floor to scene rendering contracts
- fallback serialization
- constitution repair of visible replies
- pipeline-level artifacts and result types

`pipeline` should define the mechanics. It should not become the transport or
provider shell.

`pipeline` should explicitly not own:

- turn policy selection
- commit ordering
- session lock/load behavior
- outbound transport semantics
- provider shell lifecycle
- general runtime wiring

### `session`

Status: `live truth`.

`session` should continue to own:

- visible transcript
- floor metadata sidecars
- plan state and plan events
- operation state
- review events
- turn-run recovery records
- outbound delivery records

### `durableagent`

Status: `live truth` with ongoing migration seams around turn reuse.

`durableagent` should continue to own:

- bounded-child registry
- live policy and drift governance
- review-artifact upload and upward synthesis
- remote control-plane protocol

It should reuse `turn` and `pipeline` rather than embedding a second turn stack.

## Awareness Ownership Matrix

Prompt-awareness fields are easy to smear across layers because they look like
"just data." This refactor should keep their ownership explicit.

- hidden-input awareness:
  owned by the layer that assembles latent context for a scoped turn; sourced
  from runtime-visible context and session/memory state, but handed into turn
  and pipeline as an input rather than ambient global state
- plan awareness:
  owned by `turn` as execution-shaping state for one scoped turn; sourced from
  `session`
- operation awareness:
  owned by `turn` as broader operational state for one scoped turn; sourced from
  `session`
- brokerage awareness:
  owned by `pipeline` because it describes conversational pressure and ratified
  execution shape across governor and face boundaries
- delivery awareness:
  split carefully:
  `turn` owns delivery intent and commit semantics
  `pipeline` owns conversational rendering modes that affect scene authorship
  `runtime` owns transport-capability facts
- process-shell awareness:
  owned by `runtime`; includes degraded transport state, startup/recovery status,
  and other long-lived house conditions that are not specific to one turn

When a field's owner is unclear, the default answer should be "do not add it
yet" rather than smearing it into runtime awareness.

## Workstreams

### 1. Freeze The Live Turn Contract

Before moving ownership, codify the current turn invariants as tests and small
contracts:

- governor result becomes floor plus visible scene
- transcript stores scene while sidecars store floor
- persist-before-deliver ordering for non-streaming turns
- explicit streaming delivery behavior
- constitution repair remains inside the floor boundary
- review-event generation remains post-turn
- hidden-input provenance remains attached to floor metadata

This work should mostly add tests around existing runtime behavior and promote
shared structs where the runtime already has stable concepts.

### 2. Extract Pipeline Contracts First

Move the most self-contained conversational mechanics into `pipeline`:

- brokerage artifacts and ratification result types
- floor extraction contracts and helpers
- scene rendering request/result types
- fallback serializers
- constitution repair helpers

The extraction order should favor pure or near-pure helpers first, then move the
thin orchestration wrappers that call them.

The initial target is not to remove all runtime usage. The initial target is to
make `pipeline` the obvious owner of these mechanics.

This workstream should be considered failed if `pipeline` becomes a second mixed
runtime package. If a mechanic cannot be described as a governor/face
transformation, it probably does not belong here.

### 3. Promote `turn` From Design-Lab To Interactive Owner

Make `turn` the production owner of the interactive DM golden path.

That means `turn` should accept:

- inbound message or prepared request
- scoped session snapshot
- execution policy
- ports for governor, face, persistence, and delivery

And it should return:

- turn result
- committed scene/floor outcome
- outbound intent or recorded delivery result
- turn-run facts for recovery and audit

`runtime.HandleInbound` should shrink into:

- principal and scope resolution
- session lock and load
- port assembly
- delegation into `turn`

### 4. Unify Durable Telegram Child Turns

The durable Telegram child path should move onto the same turn engine where its
stage order matches the interactive flow.

Shared machinery should include:

- prepare inbound turn
- hidden-input assembly
- brokerage and governor execution
- floor extraction and scene staging
- persistence and outbound commit ordering

Durable-agent-specific policy should remain outside the shared turn core:

- child scope resolution
- local reply permission
- review artifact upload
- policy-offered / policy-applied bookkeeping

The goal is to remove the current duplicated turn skeleton between
`runtime/turn.go` and `runtime/durable_group.go`.

This is a unification target for shared lifecycle, not a claim that durable
children are merely interactive turns with a flag flipped.

### 5. Normalize Commit And Delivery Ports

Make commit behavior explicit in `turn` rather than scattered across runtime:

- persist transcript and sidecars
- deliver outbound text / media / voice / stream
- record outbound delivery ids
- enqueue review events

The turn engine should decide the order.
The runtime shell should provide the concrete implementations.

This work should preserve the existing distinction between:

- persisted visible scene
- stored floor sidecar
- actual outbound transport result

This workstream is important enough to treat as its own constitutional subproject.
It should define and test:

- persist-before-deliver vs stream-first cases
- what counts as "delivered"
- when outbound ids are recorded
- how review-event enqueueing relates to delivery success
- what happens when persistence succeeds but delivery fails
- what happens when streaming begins but finalization fails

### 6. Move Shared Awareness Assembly Behind Clear Boundaries

The current runtime assembles multiple awareness layers:

- hidden inputs
- plan state
- operation state
- brokerage state
- delivery mode

Keep the underlying data sources where they belong, but move the assembly rules
to the boundary that actually owns them:

- turn-level awareness assembly in `turn`
- conversational transformation awareness in `pipeline`
- runtime-only process shell facts in `runtime`

This keeps prompt wiring aligned with ownership instead of letting `runtime`
remain the ambient owner of every awareness block.

### 7. Reframe Heartbeat, Cron, And Recovery As Turn Species

Heartbeat, cron, and startup recovery should not necessarily share the exact
same path as an interactive DM, but they should reuse the same turn species model.

The plan here is:

- define explicit non-interactive turn species in `turn`
- reuse shared governor/floor/scene mechanics where valid
- keep delivery policy separate per species
- keep maintenance-only side effects outside ordinary interactive paths

Heartbeat and cron should share more with each other than either shares with
interactive Telegram delivery.

Startup recovery should reuse the same turn-run and delivery discipline without
pretending it is just another chat turn.

Turn species should be introduced as:

- a shared classification vocabulary
- a place for species-specific policy and commit differences to remain explicit
- a way to reuse only the stages that are actually common

They should not be introduced as proof that one engine already handles every
species elegantly.

### 8. Clean Runtime Back Down To The House Shell

Once interactive and durable child turns are routed through `turn`, `runtime`
should be reduced to:

- house startup
- transport wiring
- background loops
- durable-agent lifecycle and wake mechanics
- process-level observability
- concrete dependency assembly

This is the point where the repo structure actually matches the architecture
statement.

### 9. Rebalance Tests Around The New Ownership Model

As ownership moves, tests should move with it.

Target test distribution:

- `pipeline`: brokerage, floor extraction, scene/fallback, constitution repair
- `turn`: policy, stage order, commit ordering, cross-species engine behavior
- `runtime`: process shell wiring, loop behavior, transport integration, recovery scheduling
- `durableagent`: policy, review, bounded-child control plane

The repo should stop using `runtime/runtime_test.go` as the default home for
every architectural invariant.

### 10. Finish The Migration With Documentation And Deletion

At the end of the refactor:

- update package docs so they describe live ownership, not just intended ownership
- remove or inline runtime helpers that are no longer the correct owner
- update architecture diagrams and readmes to reflect the new live shape
- keep only one authoritative path for each major turn species

The migration is not complete while the old runtime path and the new turn path
both remain first-class production owners.

## Suggested Extraction Order

1. Codify current invariants in tests.
2. Extract pipeline-level pure helpers and contracts.
3. Carve commit and delivery semantics into explicit contracts and tests.
4. Route interactive DM turns through `turn`.
5. Route durable Telegram child turns through `turn`.
6. Re-express heartbeat, cron, and recovery as explicit turn species.
7. Remove duplicated runtime-owned turn skeletons.
8. Rewrite package docs to describe the now-live ownership model.

## Non-Goals

- No big-bang rewrite.
- No user-facing rename of Idolum / Aphelion semantics.
- No collapse of floor and scene into one artifact.
- No second transport-specific turn engine.
- No permanent duplication between `runtime` and `turn`.

## Done Means

This refactor should be considered complete when all of the following are true:

- `runtime` is the process shell, not the main owner of one-turn stage order
- `turn` owns production turn orchestration for the major turn species
- `pipeline` owns production brokerage/floor/scene/fallback mechanics
- `pipeline` has not become a renamed mixed runtime package
- interactive and durable Telegram child turns no longer duplicate the same turn skeleton
- heartbeat, cron, and recovery use explicit turn species without pretending their
  delivery and authority semantics are identical to interactive DMs
- awareness-field ownership is explicit rather than ambient
- commit semantics are centralized, tested, and treated as constitutional behavior
- test ownership follows package ownership
- package docs describe the live architecture accurately
