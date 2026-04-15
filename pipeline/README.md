# pipeline

`pipeline` defines the intended boundary for Aphelion's **governor/face conversational mechanics**.

If `turn` is the spine of one turn, `pipeline` is the set of conversational transformations that spine invokes.

The staged plan for making that ownership real lives in
[`requirements/turn-pipeline-refactor.md`](../requirements/turn-pipeline-refactor.md).

It exists to answer questions like:

- how does Idolum propose posture before execution?
- how does Aphelion ratify or reject that posture?
- how does governor output become a floor?
- how does a floor become a scene?
- how does degraded fallback remain honest when scene authorship is unavailable?

## What `pipeline` is for

`pipeline` is meant to hold the machinery around these architectural nouns:

- **brokerage** — bounded pre-turn negotiation between Idolum and Aphelion
- **floor** — governor-owned material artifact for a turn
- **scene** — user-visible reply authored by Idolum from that floor
- **fallback** — degraded direct delivery path when ordinary scene authorship is skipped or fails

In the intended architecture:

- `runtime` owns process shell and channel wiring
- `turn` owns the state machine and stage order of one turn
- `pipeline` owns the governor/face mechanics that happen inside those stages

## What `pipeline` is not for

`pipeline` should not become:

- the process shell
- the session persistence layer
- the Telegram transport layer
- the full runtime loop
- a second copy of `turn`

If some code is about **how a turn proceeds**, it probably belongs in `turn`.
If some code is about **how conversational material is transformed across governor and face boundaries**, it probably belongs here.

## Current status

At the moment, `pipeline` is still a **design-lab package**.

The live implementations of these ideas mostly still sit in:

- `runtime/brokerage.go`
- `runtime/material.go`
- `face/provider.go`
- `face/fallback.go`
- parts of `prompt/`

This package names the boundary before the extraction fully happens.

## Why this package exists

Without a package like this, `turn` risks becoming too fat.

If `turn` owns:

- policy
- stage order
- commit semantics
- brokerage parsing
- floor parsing
- scene rendering
- fallback serialization

then it stops being an orchestration package and turns into another mixed runtime package.

`pipeline` exists to prevent that collapse.

## Relationship to the wider field

`pipeline` sits at the intersection of:

- dialogue management
- natural language generation pipelines
- governed / constitutional AI design
- agent architecture with role separation
- prompt/runtime contract design

Classic NLG literature often separates:

- content determination
- document planning
- surface realization

Aphelion's version is not identical, but it rhymes:

- governor materialization of a floor
- face-side staging of a scene
- explicit degraded fallback when ordinary staging is unavailable

What is unusual here is that these transformations are also constitutional and relational, not just stylistic.

## Likely paths forward

See [`requirements/turn-pipeline-refactor.md`](../requirements/turn-pipeline-refactor.md)
for the current staged extraction plan across the live runtime and the target
`turn` / `pipeline` split.

### 1. Keep `pipeline` as a contract package for now

Use it to define:

- brokerage artifacts
- floor extraction contracts
- scene authorship contracts
- fallback contracts

before moving any live code.

### 2. Extract floor and fallback first

A likely first real extraction path is:

- move floor parsing and floor result types here
- move fallback serialization contracts here

Those are already fairly self-contained.

### 3. Extract brokerage next

Brokerage is another good candidate because it is already conceptually bounded:

- face-side proposal
- governor-side ratification
- negotiated artifact

### 4. Keep face and provider implementations separate

`pipeline` should probably define the contracts for scene authorship without swallowing the concrete `face` package itself.

That is: `face` remains an implementation package for Idolum-facing providers and helpers; `pipeline` becomes the architectural contract layer those implementations satisfy.

## Guiding design rule

A good test for this package is:

> Is this code about transforming conversational material across governor and face boundaries?

If yes, it likely belongs here.

If it is instead about:

- when a turn runs
- who owns session locking
- how persistence happens
- how Telegram sends bytes

then it likely belongs elsewhere.
