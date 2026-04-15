# turn

`turn` defines the intended boundary for **single-turn orchestration** in Aphelion.

This package exists because the current runtime loop does more than transport messages or call a model. One conversational turn in Aphelion has a real lifecycle:

1. an inbound event arrives in a scoped session
2. the system decides what kind of turn this is
3. the face may exert conversational pressure before execution
4. the governor executes under the current policy and constraints
5. the governor-owned floor is separated from the visible scene
6. the visible reply is staged, persisted, and delivered

The long-term purpose of `turn` is to own that state machine.

The staged plan for making that ownership real lives in
[`requirements/turn-pipeline-refactor.md`](../requirements/turn-pipeline-refactor.md).

## What `turn` is for

`turn` is not meant to be:

- the process shell
- the prompt builder
- the Telegram transport layer
- the full governor implementation
- the full face implementation

It is meant to be the **spine of one turn**.

In the intended architecture:

- `runtime` owns the long-lived house process, channel wiring, startup/recovery loops, and process-level concerns
- `turn` owns the order in which a single turn proceeds
- a future `pipeline` boundary would own governor/face conversational mechanics such as brokerage, floor extraction, scene authorship, and fallback rendering

So the core question for `turn` is:

> Given one inbound event and one scoped session, how does a turn move from arrival to visible outcome?

## Why this package exists

Right now, most of that lifecycle still lives inside `runtime.HandleInbound` and adjacent helpers.

That is workable, but it mixes three different concerns:

- the long-lived runtime shell
- the orchestration state machine for a turn
- the governor/face execution pipeline itself

`turn` is a northbound extraction surface for separating those concerns without losing the actual shape of the system.

## Current status

At the moment, `turn` is still a **design-lab package**.

It names the boundary, carries an explicit `Machine` implementation for a golden-path turn, and makes the major seams visible:

- policy
- stage ordering
- governor port
- face port
- persistence port
- delivery port
- commit ordering

It is **not yet the production owner** of the live runtime loop.

## Relationship to the wider field

`turn` sits at the intersection of several fields of study and engineering practice.

### Workflow and orchestration systems

A turn is a bounded unit of work with explicit stages, transitions, and side effects.

This puts `turn` close to workflow engines and orchestration systems:

- a unit of work enters
- state evolves across a known sequence
- durable and external side effects need explicit ordering
- interruption and retry matter

### Dialogue management

From the perspective of conversational systems, `turn` is a dialogue-management layer.

Its responsibilities are not just text generation. They include:

- deciding how a turn should move
- deciding whether posture negotiation is needed
- preserving the difference between machine truth and visible reply
- making delivery and degradation explicit

### Agent architecture

In agent terms, `turn` is the layer that coordinates the relationship between:

- face-side conversational pressure
- governor-side execution
- explicit tools and side effects
- visible delivery

This places it near planner/executor and reflective-agent architectures, but with a stronger constitutional split than most agent stacks use.

### OS / supervision / distributed systems

Turns can fail, be interrupted, recover, or run in distinct scopes.

That gives `turn` an affinity with:

- supervision trees
- process orchestration
- transaction and commit ordering
- recovery-aware state machines

### Alignment and constitutional design

Aphelion is not a single undifferentiated assistant.

The governor/face split, the floor/scene split, and explicit degraded delivery all mean that `turn` also belongs to the design space of constitutional and governed AI systems.

## What is unusual here

A lot of systems treat a turn as:

- receive user input
- call model
- send answer

Aphelion does not.

A turn here is load-bearing because it has to preserve:

- scope
- authority
- relationship
- truth
- visible scene
- durability
- recovery

That makes `turn` more than a helper package. If it matures fully, it becomes one of the core packages in the repo.

## Likely paths forward

See [`requirements/turn-pipeline-refactor.md`](../requirements/turn-pipeline-refactor.md)
for the current staged extraction plan across `runtime`, `turn`, `pipeline`,
`session`, and `durableagent`.

There are a few plausible northbound paths for this package.

### 1. Keep it as a design-lab contract for a while

This is the conservative path.

Use `turn` to sharpen vocabulary, interfaces, and tests before extracting any production flow into it.

Good when:

- the architecture is still evolving quickly
- you want to discover real seams before moving code

### 2. Let `turn` own the interactive DM golden path first

This is probably the cleanest first extraction.

Start with the main interactive DM turn and let `turn` own:

- policy selection
- proposal/brokerage sequencing
- governor handoff
- scene staging
- commit/delivery ordering

Keep runtime responsible for:

- channel ingress
- session locking
- process lifecycle
- background loops

### 3. Let `turn` become a multi-species turn engine

A stronger future is for `turn` to own the shared state machine across:

- interactive turns
- heartbeat turns
- cron turns
- recovery turns
- durable child turns

That would make it the package where Aphelion's common turn logic actually lives.

### 4. Split `turn` from a future `pipeline`

If `turn` starts absorbing prompt assembly, brokerage parsing, floor extraction, and rendering mechanics directly, it will become another large mixed package.

The healthier northbound shape is likely:

- `runtime` = process shell
- `turn` = state machine / orchestration spine
- `pipeline` = governor + face mechanics

## Guiding design rule

A good test for this package is:

> Does this code describe **how one turn proceeds**, or does it describe some other neighboring concern?

If it describes:

- process lifecycle
- transport details
- provider-specific prompt mechanics
- storage implementation details

then it probably belongs somewhere else.

If it describes:

- stage order
- turn policy
- turn-level ports
- turn-level commit semantics
- the difference between floor and visible reply

then it probably belongs here.
