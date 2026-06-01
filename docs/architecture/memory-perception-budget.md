# Memory Perception Budget

`memory/` is one of Aphelion's continuity organs. Its job is not merely to
store text or retrieve similar chunks; it helps decide what remembered material
is allowed to become perception for a turn.

The design coordinate is:

> governed continuity under context scarcity.

Memory is latent until it is fed into the model context. Once admitted, it
competes with current input, tool evidence, session history, and authority text.
The memory substrate therefore needs accounting: what entered, what stayed out,
why, and under which epistemic label.

## Shape

The current memory path is best understood as a governed pipeline:

```text
raw sessions / archives / imports / curated files
        ↓
provenance + quarantine / approval / rejection
        ↓
semantic recall or curated prompt-facing selection
        ↓
posture-specific perception budget
        ↓
admitted and suppressed context layers
        ↓
model inference
```

The important boundary is that retrieval is not truth. Semantic recall can make a
fragment available, but the fragment remains a recalled source with provenance,
staleness, authority, and distortion risk. Imported material is not identity just
because it was found; it has to survive review and labeling.

## Perception Budget Contract

`memory/perception_budget.go` introduces a measurable accounting contract for
pre-inference context shape. It does not change live prompt assembly by itself;
it records how a context assembler can choose and attest memory layers.

A contract records:

- the active posture, such as `implementation`, `repair`, `reflective`,
  `durable_goal`, or `diagnostic`;
- candidate layers, such as current input, tool evidence, curated memory,
  semantic recall, rhizome, dreams, and imported archive material;
- each layer's epistemic status: binding, observed, current, curated, recalled,
  imported, motif, or hypothesis;
- estimated token cost, memory budget, total budget, and remaining headroom;
- admitted layers with admission reasons and priorities;
- suppressed layers with suppression reasons;
- low-authority labels for motif/hypothesis material;
- risks such as required current/tool evidence exceeding the nominal budget.

This makes the perceptual field auditable without pretending we can prove the
model will reason wisely from it.

## Posture, Not Fixed Camera

A deterministic memory economy can freeze the system into one viewpoint. Aphelion
should instead keep memory perception mobile while keeping the authority floor
fixed.

Binding constraints do not drift with posture:

- authority and policy remain binding;
- current user input remains the live attentional center;
- fresh tool evidence remains privileged for factual claims;
- memory never grants deploy, restart, public-contact, purchase, credential, or
  policy authority.

Memory layers do move by posture:

| Posture | Memory aperture | Rhizome / dreams treatment |
| --- | --- | --- |
| `implementation` | lean, precision-biased | suppressed unless explicitly required |
| `repair` | lean, evidence-biased | suppressed unless directly relevant |
| `reflective` | wider, continuity-aware | admitted as low-authority motif/hypothesis |
| `durable_goal` | continuity-seeking | deliberately near, still low-authority |
| `diagnostic` | broad but provenance-heavy | admitted only with labels and caution |

This is the difference between hiding motifs and governing motifs. `rhizome` and
`dreams` are valuable for thread revival, durable-goal detection, and creative or
reflective synthesis. They are dangerous when they become ambient authority or
make every operational task feel mythically important.

## Layer Semantics

A perception layer is not only text. It is text plus role, source, status, cost,
and reason.

Examples:

- `current_input` is `current`: it is the live turn signal.
- `tool_evidence` is `observed`: it is privileged for factual claims when fresh.
- `curated_memory` is `curated`: durable, but still revisable.
- `semantic_recall` is `recalled`: relevant, not automatically true.
- `rhizome` is `motif`: associative pressure, not instruction.
- `dreams` is `hypothesis`: emergent pattern, not settled fact.
- `imported_archive` is `imported`: provenance-heavy and review-dependent.

The same retrieved text may be useful in one posture and distortion in another.
The contract makes that movement explicit.

## Measurement Strategy

The first measurable claim is not that outcomes are better. It is that the
assembled context shape obeys invariants.

Current tests prove these shape constraints:

- implementation posture suppresses rhizome/dream motif layers while preserving
  current input and tool evidence;
- durable-goal posture admits rhizome/dreams as low-authority motif/hypothesis
  layers;
- reflective posture admits motif layers but still enforces memory caps;
- quarantined or rejected imports do not enter perception;
- required current input and tool evidence may exceed caps, but the contract
  reports over-budget risk instead of silently displacing them.

Later work can attach the contract to real prompt/runtime assembly, then add
replay tests and live evaluation. The immediate value is shape discipline:
Aphelion can say what kind of memory it offered to inference and why.

## Field Coordinates

This substrate is not an implementation of one specific paper or literature
line. It sits across several fields:

- information retrieval: relevance, ranking, chunking, and recall;
- archives and records management: provenance, review state, and retention;
- source monitoring: not confusing where a claim came from;
- cognitive load and working-memory research: context scarcity and displacement;
- rate-distortion / rational inattention: the cost of compressing perception;
- AI memory and RAG: retrieved context as model input;
- governance and capability systems: memory is not authority.

Aphelion's coordinate across those fields is narrower: make continuity useful
without letting remembered material possess the present.

## Non-goals

The perception budget substrate does not:

- decide Telegram UX or transport behavior;
- perform provider calls;
- execute tools;
- grant operator authority;
- write curated memory;
- turn semantic recall into fact;
- make imported archives part of identity automatically;
- replace current input or tool evidence with memory texture.

The contract is a measuring instrument and boundary surface. Runtime/prompt
assembly can later consume it, but the first responsibility is to keep memory's
cost, source, posture, and authority visible.
