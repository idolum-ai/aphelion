# Architecture Docs

This directory is the live architecture map for the current codebase.

- `requirements/` remains the normative behavior spec.
- `docs/architecture/` describes how that behavior is implemented in code today.

If these diverge, fix one of them in the same change.

## Surface Truth Classes

Use only these four terms when classifying architecture surfaces:

- `canonical`: authoritative source for a specific question.
- `projection`: rendered/derived view with no independent authority.
- `operational current-state store`: mutable "what is currently declared now"
  surface used by runtime operations.
- `compatibility fallback`: transitional store kept for migration/recovery until
  canonical or operational surfaces fully cover the use case.

## Normative Map

- [package-ownership.md](package-ownership.md): runtime/turn/pipeline ownership boundaries.
- [turn-lifecycle.md](turn-lifecycle.md): stage order across interactive, maintenance, and durable-child turns.
- [constitution-and-delivery.md](constitution-and-delivery.md): floor/scene and commit/delivery invariants.
- [durable-children.md](durable-children.md): bounded child topology and adapters.
- [state-surfaces.md](state-surfaces.md): transcript, sidecars, and operational state.
- [coordinator-boundary-audit.md](coordinator-boundary-audit.md): adapter-boundary readout and wrapper decisions.
- [migration-appendix.md](migration-appendix.md): refactor closeout and present-vs-intended reference.
- [diagrams/README.md](diagrams/README.md): canonical and archived diagram assets.

## Draft Roadmaps (Non-Normative)

- [canonical-state-and-autonomy-roadmap.md](canonical-state-and-autonomy-roadmap.md): draft convergence map for canonical truth surfaces and safer autonomy.

Treat this section as design-direction input. It is not a normative implementation
contract until explicitly promoted into the normative map above.

## Canonical Diagrams

- [01-package-map.svg](diagrams/01-package-map.svg)
- [02-interactive-turn-sequence.svg](diagrams/02-interactive-turn-sequence.svg)
- [03-constitutional-flow.svg](diagrams/03-constitutional-flow.svg)
- [04-durable-topology.svg](diagrams/04-durable-topology.svg)
- [05-state-surfaces.svg](diagrams/05-state-surfaces.svg)
- [06-delivery-polymorphism.svg](diagrams/06-delivery-polymorphism.svg)
- [07-present-vs-intended.svg](diagrams/07-present-vs-intended.svg)

## Update Rule

When touching architectural behavior in `runtime`, `turn`, `pipeline`, `session`,
or `durableagent`, update the normative docs above in the same PR unless no
architecture behavior changed.

Use `make docs-architecture` to run architecture docs checks.
