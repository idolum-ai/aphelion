# Architecture Docs

This directory is the live architecture map for the current codebase.

- `requirements/` remains the normative behavior spec.
- `docs/architecture/` describes how that behavior is implemented in code today.

If these diverge, fix one of them in the same change.

## Surface Truth Classes

Use only these three terms when classifying current architecture surfaces:

- `canonical`: authoritative source for a specific question.
- `projection`: rendered/derived view with no independent authority.
- `operational current-state store`: mutable "what is currently declared now"
  surface used by runtime operations.

The applied map for current state surfaces is
[`state-surfaces.md`](state-surfaces.md#classification-matrix). Architecture
notes that classify a current surface should use these terms exactly or link to
that matrix so the vocabulary remains searchable.

## Truth-Class Invariants

These invariants are normative for architecture and requirements alignment:

- A surface claim must map to exactly one truth class for the specific question
  being answered.
- Removed surfaces should be deleted or rejected, not remain as live inputs.
- Operator projections (`/status`, `/health trace`, quick-read) must preserve source
  attribution for canonical and operational data they render.

## Normative Map

- [design-principles.md](design-principles.md): project-level design principles for Aphelion as a governed, locally operable outpost.
- [influences-and-departures.md](influences-and-departures.md): attribution ledger for nearby systems, theory, and the points where Aphelion deliberately diverges.
- [agency-evaluation-methodology.md](agency-evaluation-methodology.md): grounded behavioral methodology for measuring agency prompt quality, drift, and hard authority failures.
- [per-role-model-evaluation.md](per-role-model-evaluation.md): draft methodology for evidence-backed model bakeoffs and per-role cost/quality frontiers.
- [principle-debt.md](principle-debt.md): active implementation gaps against the design principles.
- [package-ownership.md](package-ownership.md): runtime/turn/pipeline ownership boundaries.
- [memory-perception-budget.md](memory-perception-budget.md): memory as governed perception under context scarcity, with posture-specific layer accounting.
- [turn-lifecycle.md](turn-lifecycle.md): stage order across interactive, maintenance, and durable-child turns.
- [action-proposal-continuation-lease.md](action-proposal-continuation-lease.md): typed bounded action proposals and consumable continuation leases.
- [constitution-and-delivery.md](constitution-and-delivery.md): floor/scene and commit/delivery invariants.
- [operator-presentation-contract.md](operator-presentation-contract.md): human Telegram/CLI presentation contract for status, rationale, next action, details, and evidence.
- [routing-based-face-prompting.md](routing-based-face-prompting.md): route-first face prompt assembly and nondeterministic persona eval invariants.
- [durable-children.md](durable-children.md): bounded child topology and adapters.
- [thread-native-durable-work.md](thread-native-durable-work.md): exploratory direction for making threads the operator-facing durable work primitive while keeping authority typed.
- [universal-evidence-ledger.md](universal-evidence-ledger.md): immutable typed evidence objects, audited hydration, and long-horizon context-fidelity invariants.
- [effect-attempt-ledger.md](effect-attempt-ledger.md): durable side-effect attempt lifecycle for retry-safe completion and verification.
- [state-surfaces.md](state-surfaces.md): transcript, sidecars, and operational state.
- [transparent-execution-sequence.md](transparent-execution-sequence.md): canonical execution timeline and projection/fallback precedence.
- [external-tools-pilot.md](external-tools-pilot.md): current external-tool lifecycle, execution-mode semantics, and bundled `browse_page` pilot.
- [telegram-child-bot-runbook.md](telegram-child-bot-runbook.md): generic Telegram child-bot runner boundary and operational checks.
- [capability-delegation-lane.md](capability-delegation-lane.md): general request/review/grant lane for tools, devices, accounts, purchases, public web, and emergent permissions.
- [structural-hygiene.md](structural-hygiene.md): large-file ledger and split discipline.
- [diagrams/README.md](diagrams/README.md): canonical diagram assets.

## Canonical Diagrams

- [01-package-map.svg](diagrams/01-package-map.svg)
- [02-interactive-turn-sequence.svg](diagrams/02-interactive-turn-sequence.svg)
- [03-constitutional-flow.svg](diagrams/03-constitutional-flow.svg)
- [04-durable-topology.svg](diagrams/04-durable-topology.svg)
- [05-state-surfaces.svg](diagrams/05-state-surfaces.svg)
- [06-delivery-polymorphism.svg](diagrams/06-delivery-polymorphism.svg)

## Update Rule

When touching architectural behavior in `runtime`, `turn`, `pipeline`, `session`,
or `durableagent`, update the normative docs above in the same PR unless no
architecture behavior changed.

Use `make docs-architecture` to run architecture docs checks.
