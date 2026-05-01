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

## Truth-Class Invariants

These invariants are normative for architecture and requirements alignment:

- A surface claim must map to exactly one truth class for the specific question
  being answered.
- `compatibility fallback` may be used only when the matching canonical and
  operational current-state coverage is missing or incomplete.
- `compatibility fallback` must never override available canonical truth or
  operational current-state declarations for the same question.
- Operator projections (`/status`, `/debug`, quick-read) must preserve source
  attribution when compatibility fallback data is used.

## Normative Map

- [package-ownership.md](package-ownership.md): runtime/turn/pipeline ownership boundaries.
- [turn-lifecycle.md](turn-lifecycle.md): stage order across interactive, maintenance, and durable-child turns.
- [action-proposal-continuation-lease.md](action-proposal-continuation-lease.md): typed bounded action proposals and consumable continuation leases.
- [constitution-and-delivery.md](constitution-and-delivery.md): floor/scene and commit/delivery invariants.
- [durable-children.md](durable-children.md): bounded child topology and adapters.
- [state-surfaces.md](state-surfaces.md): transcript, sidecars, and operational state.
- [transparent-execution-sequence.md](transparent-execution-sequence.md): canonical execution timeline and projection/fallback precedence.
- [coordinator-boundary-audit.md](coordinator-boundary-audit.md): adapter-boundary readout and wrapper decisions.
- [external-tools-pilot.md](external-tools-pilot.md): current external-tool lifecycle, execution-mode semantics, and bundled `browse_page` pilot.
- [capability-delegation-lane.md](capability-delegation-lane.md): general request/review/grant lane for tools, devices, accounts, purchases, public web, and emergent permissions.
- [migration-appendix.md](migration-appendix.md): refactor closeout and present-vs-intended reference.
- [diagrams/README.md](diagrams/README.md): canonical diagram assets.

## Draft Roadmaps (Non-Normative)

- [canonical-state-and-autonomy-roadmap.md](canonical-state-and-autonomy-roadmap.md): draft convergence map for canonical truth surfaces and safer autonomy.
- [mission-ledger-roadmap.md](mission-ledger-roadmap.md): draft plan for durable missions, self-summon as review, and bounded self-continuation without hidden authority.
- [organic-agent-owned-tools-proposal.md](organic-agent-owned-tools-proposal.md): draft proposal for moving from core-embedded domain tools toward agent-owned external tools with governed install and attestation.
- [tailscale-agent-substrate-project.md](tailscale-agent-substrate-project.md): draft project plan for making Tailscale/tsnet a first-class private network substrate for Aphelion and durable children.

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
