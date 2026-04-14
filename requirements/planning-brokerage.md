# Planning Brokerage — Bounded Pre-Turn Negotiation

## Overview

Some turns should not move directly from user input to governor execution.

For open-ended, strategic, ambiguous, emotionally charged, or repo-inspection-heavy turns, Aphelion should run a bounded **planning brokerage** before the main turn executes.

This brokerage is not a free-form internal conversation. It is a short negotiation:

1. `Idolum` states what conversational pressure it wants to exert
2. `Aphelion` answers with what system posture it can actually ratify
3. the normal governor/tool turn executes under the negotiated artifact and later emits a bounded material floor for Idolum to stage

This preserves `Idolum`'s initiative without making the runtime a committee, and preserves `Aphelion`'s authority without flattening `Idolum` into polite advice.

## Telos

The brokerage layer exists to improve:

- initiative without overreach
- legibility of how the system chose to proceed
- alignment between relational instinct and operational discipline
- tool-use decisions on ambiguous turns

It should not create bureaucracy, self-chat, or a second visible conversation.

It should also prevent a silent collapse back into the older shape where the governor writes most of the visible answer and the face merely softens it afterward.

## Scope

### v0 required

- bounded pre-turn brokerage for selected interactive turns
- `Idolum` brokerage proposal
- `Aphelion` ratification pass
- negotiated brokerage block injected into the main governor turn
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
- what execution shape this turn seems to need
- whether the system should inspect, ask before acting, or answer now
- what hidden input or latent signal is shaping that read when one is materially present
- what tone or initiative would improve the turn

`Idolum` does not ratify or authorize. It proposes.

### Aphelion

`Aphelion` owns ratification.

It should decide:

- which execution contract is actually warranted
- which parts of Idolum's push are accepted, adapted, or rejected
- whether tools are needed
- whether clarification is required first

The authoritative execution boundary still belongs to Aphelion, but the main turn should preserve both sides of the brokerage rather than only the final ratified compression.

Aphelion's post-brokerage task is not just "answer." Its task is to decide the material floor the face is later allowed to stage.

## Execution Contract

The brokerage layer should prefer a small execution contract over a named turn-mode taxonomy.

- `INSPECT: yes|no`
- `QUESTION: yes|no`
- `ANSWER: yes|no`

## Idolum Brokerage Proposal

The face-side brokerage output should be short and bounded. It may be structured when useful, but it should not feel like bureaucracy by default. When a hidden input is materially shaping the push, the note should name it. See `hidden-inputs.md`.

Example shape:

```text
INSPECT: yes
QUESTION: no
ANSWER: yes
WHY: The user wants grounded feature ideas, not generic brainstorming.
PUSH:
- Inspect the repo before proposing features.
- Favor concrete gaps over speculative wishlists.
- Keep the tone energetic and high-agency.
```

This note is non-authoritative on execution, but it is not disposable. It represents the conversational pressure the runtime should preserve when possible.

## Aphelion Ratification Pass

Before the main governor/tool turn, `Aphelion` should run a short tool-free planning pass that sees:

- the normal machine-owned governor prompt
- the latest user input
- compacted history if needed
- `Idolum`'s brokerage proposal

It should return a short structured ratification.

On Aphelion's side, this ratification must be parseable enough for runtime execution. That machine contract belongs to the governor artifact, not to brokerage as a whole.

Example shape:

```text
INSPECT: yes
QUESTION: no
ANSWER: yes
RATIFICATION: adapt
PLAN:
- Inspect prompt, runtime, and memory integration surfaces first.
- Then propose features grounded in the current architecture.
- Keep the answer concrete and prioritized.
```

Aphelion's ratification remains authoritative for execution boundaries, but the main turn should carry the negotiated brokerage block rather than only the ratified compression.

### Required ratification fields

These constraints apply to Aphelion's ratification artifact so runtime can execute cleanly. They should not be projected backward onto Idolum's side of the brokerage, which may remain more conversational and bounded.

The runtime should parse and carry these fields explicitly:

- `INSPECT`
- `QUESTION`
- `ANSWER`
- `RATIFICATION`
- `SIGNAL_JUDGMENT` — optional; present when Idolum named a hidden input in its proposal. Aphelion states whether the signal is confirmed, overridden, or not material. Preserves Aphelion's side of the signal negotiation in the artifact.
- `PLAN` steps

`RATIFICATION` uses a small fixed vocabulary:

- `accept`
- `adapt`
- `reject`

`PLAN` should usually contain one to three short concrete steps.

If the ratification output is missing required fields or yields no usable steps, the runtime should treat it as an invalid brokerage artifact and fall back through the normal proposal path rather than passing an ambiguous blob into the governor.

## Main Turn Execution

After ratification, the normal governor/tool turn should run as usual.

The main turn should receive:

- the standard governor prompt
- the structured negotiated brokerage artifact as machine-scoped context
- compacted history
- the latest user input

The main turn should then produce:

- the governor-owned material floor
- not, by default, the full user-visible scene

The raw `Idolum` brokerage position should survive into the negotiated brokerage block when a valid ratification exists. Disagreement is signal, not noise.

The negotiated brokerage block should preserve both:

- `Idolum`'s raw brokerage push
- `Aphelion`'s parsed ratification fields and bounded execution steps

That negotiated block exists to constrain both later phases:

- how Aphelion materializes the turn
- how Idolum stages the visible scene

The governor should be able to see, explicitly:

- the ratified execution contract
- the ratification disposition
- the concrete execution steps
- the original ratification record

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
- Idolum's suggested execution contract when available
- Aphelion's ratified execution contract when available
- Aphelion's ratification disposition when available

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

If the governor ratification pass returns an invalid structure:

- treat it the same as a ratification failure
- do not pass the malformed ratification blob forward as if it were a valid negotiated plan

The system must not drop or stall a turn merely because brokerage failed.

## Decisions

- **Brokerage is bounded.** The system may iterate through a small number of bounded revision rounds, then either converge or fail closed to the plain proposal path.
- **Idolum proposes posture.** It does not authorize tools or system actions.
- **Aphelion ratifies execution.** It remains the action and authority layer.
- **Brokerage preserves both pressures.** The surviving artifact should keep Idolum's push and Aphelion's ratification together.
- **Ratification must be parseable.** `INSPECT`, `QUESTION`, `ANSWER`, `RATIFICATION`, and `PLAN` are runtime contract fields, not just suggestive formatting.
- **Brokerage is selective.** It should not run on every turn.
- **The negotiated brokerage block is machine-scoped context.** It is not user-visible by default.
- **Fallback is required and honest.** Brokerage failure must degrade to the existing turn path without relabeling brokerage text as some other artifact type.

## Deferred

Still deferred after this tranche:

- mid-turn re-ratification after reconnaissance
- persistent plan-step tracking during execution
- user-visible surfaced brokerage plans by default
- multi-round brokerage loops
- explicit brokerage constraints for the material-floor output schema

## Test Plan

- **TestBrokerageActivatesForStrategicInteractiveTurn**: feature/codebase-style requests trigger brokerage
- **TestBrokerageSkipsSimpleFactualTurn**: simple factual questions skip brokerage
- **TestBrokerageNegotiatedBlockFeedsMainGovernorTurn**: the negotiated brokerage block enters the main governor turn
- **TestBrokerageRatificationParsesDispositionAndSteps**: a valid ratification yields an explicit execution contract, disposition, and bounded steps
- **TestBrokerageInvalidRatificationFallsBackToProposal**: malformed ratification output triggers the same fallback ladder as a ratification error
- **TestBrokerageRerunsPlainProposalAfterRatificationFailure**: failed ratification triggers a real proposal rerun
- **TestBrokeragePreservesFramingWhenProposalRerunFails**: failed proposal rerun preserves brokerage framing instead of relabeling it
- **TestBrokerageAwarenessVisibleToGovernorAndFace**: runtime awareness reflects brokerage phase and the ratified execution contract
