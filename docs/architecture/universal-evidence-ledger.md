# Universal Evidence Ledger

_Status: normative architecture direction._

Long-horizon agent work fails when each turn reasons mostly from the prior
turn's interpretation. That creates a telephone-game failure mode: summaries
become sources, small reinterpretations compound, and the system drifts away
from the facts it originally observed.

Aphelion's answer is an evidence-centric ledger. Conversation remains the
operator navigation layer, but continuation, recovery, status, and diagnosis
should rehydrate canonical evidence objects whenever the work depends on old
facts.

## Contract

An evidence object is an immutable typed snapshot of something Aphelion observed
or recorded:

- source kind and source ref
- session/scope identity
- epistemic status (`observed`, `claimed`, `projection`, `attested`, or `gap`)
- optional authority class and subject key
- bounded summary and digest
- canonical JSON payload and runtime-computed payload hash
- observed timestamp

The source row may remain mutable when it is an operational current-state store.
The evidence object is not mutable. If a later state changes, it writes a new
source ref and therefore a new evidence object.

## Source Classes

The ledger does not replace existing stores; it indexes them as evidence.

- Canonical sources such as `execution_events`, `messages`, delivered review
  records, curiosity observations, and artifacts become evidence objects with
  observed or attested status.
- Operational current-state stores such as operation state, plan state,
  continuation state, turn-run recovery state, and pending review/decision state
  become projection evidence snapshots.
- Model-authored assistant text remains claimed evidence unless another
  privileged component attests it.

Startup migration creates the ledger tables and seeds current session snapshots
only. Explicit historical backfill is available as a maintenance action; it is a
best-effort current snapshot of mutable JSON, not a claim that every historical
intermediate state is recoverable.

## Hydration

Evidence hydration is deterministic and audited. A hydration run receives:

- the current session/scope
- the active operation ID when available
- the current objective or question
- optional required evidence IDs
- a bounded limit

It returns selected evidence objects plus any required IDs that were missing or
out of scope. The run itself is recorded in `evidence_hydration_runs`.

Hydration must preserve the active session boundary by default. A known evidence
ID from another session is reported missing rather than rehydrated. Cross-scope
hydration is a separate explicit mode for future typed review surfaces; it must
not be ambient model recall.

If no candidate matches, hydration may fall back to the latest low-authority
state snapshots, but the fallback must be labeled as fallback. A fallback is a
repair affordance, not evidence that the old facts were recovered.

## Runtime Use

Interactive-like turn assembly should always include a tiny evidence-ledger
pointer so the model knows canonical evidence exists and can pull it through the
read-only hydration tool. Full selected evidence is not pushed into every
ordinary prompt. Automatic hydration runs when typed state or request pressure
needs source-fact fidelity: recovery, continuation, active-operation, or
an explicit request to restore prior context. Hydration blocks contain evidence
IDs, source kind, epistemic status, subject, payload hash, and bounded
summary/digest.

The model also receives a read-only `evidence_hydrate` tool. Use it when a turn
needs to ground a continuation or recovery decision in older source facts rather
than relying on a summary chain.

Cost should scale with change. Writing evidence to the ledger is cheap and
canonical; prompt admission is a projection. Repeated turns should carry stable
evidence references and hydrate only the evidence needed for the current
judgment.

## Evaluation

Long-horizon evaluation should test fidelity, not just plausible next-step
generation. Canonical cases should cover:

- state-fidelity drift: later conversational summaries conflict with original
  source evidence, and the system must prefer the original evidence object;
- iterative inference pressure: a multi-turn path keeps references to stable
  evidence IDs instead of repeating summary paraphrases as facts;
- context hydration under pressure: when recent chat context is noisy or from
  another thread, hydration must select active-session evidence and report gaps;
- authority completion: completion still requires matching execution evidence,
  not an evidence object whose epistemic status is only `claimed` or
  `projection`.

The success criterion is not that every generated continuation sounds good. The
criterion is that the system can trace a continuation claim back to stable
evidence IDs with the correct source and epistemic status.

## Non-Goals

- The ledger is not vector memory and does not make summaries authoritative.
- The ledger is not a new authority surface; evidence objects do not grant
  capabilities.
- The first retrieval mode is deterministic. Semantic ranking or judge-based
  retrieval can be added later only if evals show deterministic hydration is too
  weak.
