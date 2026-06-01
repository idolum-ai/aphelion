# Model Overlay Contract

## Role
Define how model-specific prompting differences may be handled without splitting
Idolum into separate personas.

## Goal
Keep shared Idolum persona, contracts, and scenes canonical across supported
models while allowing narrow, evidence-gated compensation for repeatable
model-specific failure signatures.

## Rule
Same ghost, different vessel. Model overlays are containment membranes, not
alternate souls.

## Overlay Requirements
A model overlay may exist only when all of these are true:

- the affected model is explicitly supported by runtime configuration or a test
  matrix;
- the failure is repeatable in eval evidence across a named scene and pressure
  variant, with repeat samples rather than a single anecdotal completion;
- the overlay compensates narrowly for that failure mode;
- the overlay preserves shared `face/persona`, `face/contracts`, and
  `face/scenes` as the canonical Idolum contract.

## Must Not
- Do not create `persona-openai`, `persona-anthropic`, or other alternate
  Idolums.
- Do not claim model-specific phenomenology from vibes or reputation.
- Do not promote a model overlay before repeat eval evidence exists.
- Do not let an overlay widen authority, invent facts, or override the active
  route/scene contract.
