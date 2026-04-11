# Planning Brokerage — Bounded Pre-Turn Negotiation

## Overview

Some turns should not move directly from user input to governor execution.

For open-ended, strategic, ambiguous, emotionally charged, or repo-inspection-heavy turns, Aphelion should run a bounded **planning brokerage** before the main turn executes.

This brokerage is not a free-form internal conversation. It is a short negotiation:

1. `Idolum` states what conversational pressure it wants to exert
2. `Aphelion` answers with what system posture it can actually ratify
3. the normal governor/tool turn executes under the negotiated artifact

This preserves `Idolum`'s initiative without making the runtime a committee, and preserves `Aphelion`'s authority without flattening `Idolum` into polite advice.

## Telos

The brokerage layer exists to improve:

- initiative without overreach
- legibility of how the system chose to proceed
- alignment between relational instinct and operational discipline
- tool-use decisions on ambiguous turns

It should not create bureaucracy, self-chat, or a second visible conversation.

## Scope

### v0 required

- bounded pre-turn brokerage for selected interactive turns
- `Idolum` brokerage proposal
- `Aphelion` ratification pass
- ratified plan injected into the main governor turn
- runtime awareness of brokerage state
- graceful fallback to the ordinary proposal path when brokerage fails or is not warranted

### Deferred after v0

- multi-round negotiation
- brokerage for heartbeat/cron
- persistence of full brokerage artifacts beyond prompt/audit surfaces
- operator-configurable brokerage heuristics

## Activation

Brokerage should only run when heuristics suggest that "how to proceed" is itself part of the work.

Good brokerage candidates:

- strategic ideation
- feature brainstorming
- repo/codebase exploration requests
- ambiguous requests with multiple plausible actions
- reflective or emotionally loaded turns where tone and direction both matter

Brokerage should usually be skipped for:

- slash commands
- simple factual questions
- straightforward mechanical/tool-output turns
- short status queries

## Brokerage Roles

### Idolum

`Idolum` owns the first move in brokerage.

It should say:

- what the user seems to need
- what kind of turn this should be
- whether the system should answer, inspect, ask, decline, or hold
- what tone or initiative would improve the turn

`Idolum` does not ratify or authorize. It proposes.

### Aphelion

`Aphelion` owns ratification.

It should decide:

- which turn mode is actually warranted
- which parts of Idolum's push are accepted, adapted, or rejected
- whether tools are needed
- whether clarification is required first

The authoritative execution boundary still belongs to Aphelion, but the main turn should preserve both sides of the brokerage rather than only the final ratified compression.

## Turn Modes

The brokerage layer should use a small fixed vocabulary.

- `answer_now`
- `inspect_then_answer`
- `ask_then_wait`
- `decline`
- `silent`

## Idolum Brokerage Proposal

The face-side brokerage output should be short and bounded. It may be structured when useful, but it should not feel like bureaucracy by default.

Example shape:

```text
MODE: inspect_then_answer
WHY: The user wants grounded feature ideas, not generic brainstorming.
PUSH:
- Inspect the repo before proposing features.
- Favor concrete gaps over speculative wishlists.
- Keep the tone energetic and high-agency.
```

This output is advisory only.

## Aphelion Ratification Pass

Before the main governor/tool turn, `Aphelion` should run a short tool-free planning pass that sees:

- the normal machine-owned governor prompt
- the latest user input
- compacted history if needed
- `Idolum`'s brokerage proposal

It should return a short structured ratification.

Example shape:

```text
MODE: inspect_then_answer
RATIFICATION: adapt
PLAN:
- Inspect prompt, runtime, and memory integration surfaces first.
- Then propose features grounded in the current architecture.
- Keep the answer concrete and prioritized.
```

This ratification is the authoritative planning artifact for the main turn.

## Main Turn Execution

After ratification, the normal governor/tool turn should run as usual.

The main turn should receive:

- the standard governor prompt
- the ratified brokerage plan as machine-scoped context
- compacted history
- the latest user input

The raw `Idolum` brokerage position should survive into the negotiated brokerage block when a valid ratification exists. Disagreement is signal, not noise.

If ratification fails, the runtime may fall back to the older advisory proposal path.

## Prompt Placement

Brokerage context belongs in machine-scoped turn-local material, not in operator memory files.

Recommended ordering:

1. governor machine header
2. stable workspace files
3. tool manifest
4. advisory tool policy
5. dynamic files
6. negotiated brokerage block when present
7. history
8. latest user message

The face-side brokerage prompt should remain face-only, and should encourage a short dialogue-like push rather than a rigid mini-protocol.

## Runtime Awareness

The machine-authored runtime awareness surface should expose:

- whether brokerage is active
- whether the current turn used plain proposal or brokerage
- Idolum's suggested turn mode when available
- Aphelion's ratified turn mode when available

`Idolum` should receive only the subset relevant to speaking honestly about the turn posture.

## Failure and Fallback

Brokerage is an optimization, not a dependency.

If the face-side brokerage proposal fails:

- continue without brokerage
- rerun the ordinary `Idolum` proposal path when proposal policy allows it

If the governor ratification pass fails:

- continue with the ordinary governor turn
- rerun a true plain-proposal pass rather than relabeling the brokerage note as proposal
- if that proposal rerun also fails, preserve the original brokerage note honestly instead of falsifying its type

The system must not drop or stall a turn merely because brokerage failed.

## Decisions

- **Brokerage is bounded.** One face proposal, one governor ratification, then execution.
- **Idolum proposes posture.** It does not authorize tools or system actions.
- **Aphelion ratifies execution.** It remains the action and authority layer.
- **Brokerage preserves both pressures.** The surviving artifact should keep Idolum's push and Aphelion's ratification together.
- **Brokerage is selective.** It should not run on every turn.
- **The negotiated brokerage block is machine-scoped context.** It is not user-visible by default.
- **Fallback is required and honest.** Brokerage failure must degrade to the existing turn path without relabeling brokerage text as some other artifact type.

## Test Plan

- **TestBrokerageActivatesForStrategicInteractiveTurn**: feature/codebase-style requests trigger brokerage
- **TestBrokerageSkipsSimpleFactualTurn**: simple factual questions skip brokerage
- **TestBrokerageRatificationFeedsMainGovernorTurn**: the ratified plan enters the main governor turn
- **TestBrokerageRerunsPlainProposalAfterRatificationFailure**: failed ratification triggers a real proposal rerun
- **TestBrokeragePreservesFramingWhenProposalRerunFails**: failed proposal rerun preserves brokerage framing instead of relabeling it
- **TestBrokerageAwarenessVisibleToGovernorAndFace**: runtime awareness reflects brokerage mode and ratified turn mode
