# Interpretation, Judgment, and Dissent Surfaces

Aphelion's risk is not that it interprets. Every agentic system must turn messy
inputs into judgments before it can decide what to perceive, show, recover,
repair, or authorize.

The risk is that correlated interpretations can become settled reality without
a path for later evidence to challenge them. Local conservatism does not compose
into global conservatism when memory admission, salience, brokerage,
constitution checks, recovery arbitration, and re-entry ranking all inherit the
same false frame. A system can be careful about a phantom.

This document names the shared design object for that problem. Consequential
interpretation should produce a `Judgment`, identify the `Ground` that supports
or could contradict it, and expose a `Challenge` path that can uphold, demote,
mark uncertain, or verify the judgment later.

`Dissent` is the system property: Aphelion can disagree with its own settled
interpretations. `Challenge` is the typed mechanism that carries one such
disagreement through recheck, adjudication, and possible demotion.

The registry at the end audits current surfaces against that model. It is a
target architecture plus a current-state map, not a claim that every listed
surface already satisfies every rule.

## Core Model

The model has three parts:

1. `Judgment`: a local interpretation with provenance, completeness,
   consequence, and freshness.
2. `Ground`: the evidence or state class that supports the judgment, plus its
   independence from the judgment's source.
3. `Challenge`: the typed path by which decorrelated ground can contest a
   settled judgment.

This is not a call for a central classifier service. Domain mechanisms should
stay local. The shared architecture work is to make their judgment and
challenge edges visible, replayable, and conservative.

## Judgment

A judgment is any local interpretation that may affect perception, salience,
control flow, durable state, presentation, or authority. It may come from a
compiler, parser, recognizer, scorer, ranker, ruleset, or model judgment.

Text may become a typed object, but typed output is not a trust upgrade by
itself. A brittle recognizer can emit a perfectly typed, completely wrong
object. Consequential judgments therefore need provenance, completeness,
failure semantics, and a declared consequence class.

Future consequential interpreters should converge on a typed judgment envelope.
The Go shape below is illustrative design notation, not a committed API:

```go
type Judgment[T any] struct {
    InterpreterID string
    Version      string
    Result       T
    Status       JudgmentStatus // complete, partial, abstain
    Unknowns     []UnknownRegion
    Evidence     []EvidenceRef
    Consequence  ConsequenceClass
    Ground       GroundClass
    CreatedAt    time.Time
    ExpiresAt    time.Time
}
```

The useful question is not "how confident was the model?" It is:

- Was the full input language covered?
- Which regions were not understood?
- Is the result conservative for its consequence?
- Which downstream consumer acted on it?
- What ground class supports it?
- Which stronger ground could contradict it?
- Can it be replayed under a newer interpreter version?

`Status` and `Unknowns` are coupled: a complete judgment should have no unknown
regions; a partial judgment should name them; an abstention should explain why
no result was safe to produce.

## Ground

Aphelion does not have absolute ground truth. It can still preserve useful
differences in ground strength:

1. explicit current operator input;
2. current durable state with lineage and version;
3. immutable evidence object with provenance and content hash;
4. effect-attempt record or verified execution outcome;
5. fresh tool observation;
6. model-authored summary or material floor;
7. heuristic judgment;
8. stale recovered context.

Ground strength and source independence are separate axes. A strong record from
the same interpretive source may be useful as continuity, but it is weak
corroboration for dissent. A weaker but decorrelated observation can be more
useful for challenging a shared false frame.

The ordering is contextual, not a universal proof calculus. A fresh tool
observation can be wrong; a user message can be ambiguous. The rule is that a
lower-ground judgment must not silently overrule a higher-ground contradiction,
and a same-source judgment must not be treated as independent corroboration.

## Challenge

Aphelion already has an argumentation layer in planning brokerage:

`face pressure -> governor ratification -> execution contract`

The face proposes how a turn should move. The governor ratifies, adapts, or
rejects that pressure. The negotiated artifact preserves both sides instead of
collapsing them into a single summary.

That pattern should be mapped onto settled beliefs:

`settled judgment -> challenge -> evidence recheck -> uphold | demote | mark uncertain | verify`

The face or another local surface may initiate dissent, but the face should not
adjudicate it alone. Dissent is useful only when it can appeal to ground that is
less correlated with the challenged judgment: current durable state, immutable
evidence, effect-attempt records, fresh tool observations, timestamps, operation
lineage, or explicit operator input.

The design object is a typed challenge record. The Go shape below is
illustrative design notation, not a committed API:

```go
type BeliefChallenge struct {
    SubjectRef       string
    ChallengedKind   string
    ChallengedBy     string
    Reason           string
    EvidenceRefs     []string
    Unknowns         []string
    Adjudicator      string // typed_ruleset, operator, eval_replay, model_advisory
    Decision         ChallengeDecision // upheld, demoted, contradicted, uncertain, needs_verification
    DecisionGround   GroundClass
    DecisionEvidence []string
    CreatedAt        time.Time
}
```

This is a design direction, not an implementation requirement for this PR. The
important architectural move is to name dissent as a first-class path. Without
it, every registry entry can become locally careful while the system remains
globally unable to challenge a coherent false premise.

Challenge decisions that affect authority, durable state, recovery priority, or
operator-visible completion must be adjudicated by typed rules over decorrelated
ground, by explicit operator disambiguation, or by replayable eval evidence. A
model judgment may initiate, summarize, or explain a challenge, but it must not
be the authority that demotes or upholds another model-shaped judgment.

Some challenge boundaries are mandatory. A model-authored or heuristic judgment
about to feed an authority-bearing, durable-state, recovery-selection, or
completion decision must be checked against at least one decorrelated ground
source before it acts, whether or not any surface "noticed" a conflict.

Decorrelated ground is not merely a second record. A judgment is not
independently corroborated by another artifact that shares the same model call,
material floor, memory summary, parser output, interpreter version, or upstream
evidence chain. The decorrelation decision is itself a judgment and should use
typed rules where it affects authority, durable state, recovery priority, or
completion.

## Rules

- No non-structural judgment may silently acquire greater consequence by being
  converted into a typed object.
- More information may narrow a judgment. Missing information may only preserve
  or increase restrictions.
- A partial interpretation must expose its unknown regions or abstain before it
  reaches an authority-bearing consumer.
- Advisory judgments may route attention, but they may not create, widen, or
  repair authority.
- Presentation judgments may render an existing record, but they must not own
  the truth behind it.
- A settled judgment that later conflicts with stronger ground must have a
  demotion or verification path.
- Model-authored or heuristic judgments that drive authority, durable state,
  recovery selection, or completion must cross a mandatory challenge boundary
  against decorrelated ground.
- Challenge decisions that affect authority, durable state, recovery priority,
  or completion must be ruleset-, operator-, or replay-adjudicated, not decided
  by another unconstrained model judgment.
- Correlated judgments must not be counted as independent evidence.
- Eval oracles are development evidence, not runtime enforcement.
- Centralize the judgment contract, challenge contract, registry,
  observability, and replay harness; keep domain mechanisms local.

Monotonicity and demotion operate at different levels. Monotonicity governs
interpretation inside the current frame: partial parsing and missing
information must not lower restrictions. Demotion governs the frame itself:
when decorrelated ground contradicts a settled premise, Aphelion may lower the
trust placed in that premise, mark it uncertain, or require verification before
acting from it.

For authority-sensitive surfaces, interpretation should produce a conservative
set or upper bound of possible consequences, not one winning label.

```text
unknown / dynamic
        ↓
multiple possible effects
        ↓
known high-impact effect
        ↓
known bounded effect
```

Authorization operates on the upper bound. It must not trust whichever branch
ranked highest. This is why shell commands with dynamic execution, multiple
side-effecting atoms, or unsupported syntax are rejected until a typed operation
or confinement contract can represent the effect truthfully.

The same rule applies outside shell execution:

- Recovery should not force ambiguous state into live or suppressed.
- Brokerage should distinguish fully parsed contracts from partial field hits.
- Memory admission should preserve provenance and epistemic status.
- Constitution checks should name recognized violations and abstentions instead
  of emitting a generic valid/invalid aura.

## Registry Profile

Do not describe interpretation mechanisms with one maturity label. Use a
multi-axis profile:

| Axis | Values |
| --- | --- |
| Input trust | `typed trusted`, `typed untrusted`, `bounded text`, `free text`, `model output`, `external payload` |
| Mechanism | `compiler`, `parser`, `recognizer`, `ruleset`, `scorer`, `ranker`, `model judgment` |
| Consequence | `presentation`, `salience`, `perception`, `control flow`, `durable state`, `authority narrowing`, `authority granting` |
| Failure semantics | `fail closed`, `conservative upper bound`, `abstain`, `defer`, `fallback`, `silent default` |
| Ground class | `operator assertion`, `current durable state`, `typed evidence`, `effect attempt`, `fresh observation`, `model summary`, `heuristic judgment`, `stale context` |
| Dissent path | `none`, `local repair`, `typed challenge`, `verification required`, `operator disambiguation`, `eval replay` |
| Assurance | `examples`, `unit tests`, `adversarial corpus`, `fuzz/property tests`, `shadow evaluation`, `production calibration` |
| Lifecycle | `experimental`, `canary`, `production`, `deprecated` |
| Compliance | `satisfies`, `partial`, `debt`, `not applicable` |

`projection` remains a state-surface truth class in
[`state-surfaces.md`](state-surfaces.md). Do not reuse it as an interpretation
maturity label.

## Current Compliance

Rows marked `satisfies` already have an appropriate challenge, verification,
or non-authority boundary for their consequence. Rows marked `partial` have a
local safety mechanism but do not yet satisfy the full target architecture.
Rows marked `debt` identify known gaps that should be tracked before the surface
is treated as a production example of this architecture. Rows marked
`not applicable` do not make settled judgments consequential enough to require
a demotion path.

The registry is hand-maintained today. Architecture review should require new
consequential interpreters to add or update a row. Future work can make anchor
existence and missing-surface detection more mechanical; until then,
completeness is review-enforced rather than fully CI-proven.

## Current Registry

The registry describes edges:

`raw source -> interpreter -> judgment -> consumer -> consequence -> trace`

| Surface | Code anchors | Input trust | Mechanism | Consequence | Ground class | Dissent path | Compliance | Failure semantics / assurance |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Shell effect planner | [`commandeffect/effect.go`](../../commandeffect/effect.go) | bounded text/free shell string | parser + ruleset | authority narrowing, control flow, durable effect-attempt state | bounded text + typed plan; later effect attempt or fresh observation outranks it | verification required when outcome/effect evidence contradicts plan; eval replay for parser drift | partial | fail closed or conservative upper bound for unknown, dynamic, or multi-authority plans; unit tests + adversarial corpus; production restricted-shell gate |
| Effect authorization | [`effectauth/effectauth.go`](../../effectauth/effectauth.go) | typed trusted envelope + command judgment | compiler/ruleset | authority narrowing/denial | current durable envelope, grant, lease, and command judgment | local repair/block for invalid contracts; operator disambiguation for missing authority | satisfies | fail closed for invalid active envelopes and disallowed effects; unit tests; production authority membrane |
| Exec approval presentation | [`tool/exec_guard.go`](../../tool/exec_guard.go) | bounded text | recognizer | presentation and operator review salience | typed effect decision should outrank presentation text | none as authority; projection should be regenerated from typed decision | not applicable | defer to typed effect decisions; proposal text is not authority; unit tests; production presentation helper |
| Authority contract compilation | [`session/authority_contract.go`](../../session/authority_contract.go), [`session/authority_contract_compiler.go`](../../session/authority_contract_compiler.go), [`session/types_continuation.go`](../../session/types_continuation.go) | typed trusted fields plus bounded effect text | compiler with limited recognizers | authority narrowing/grant constraints | current durable proposal, phase, envelope, and exact action tokens | typed repair/block when prose and actions contradict; operator disambiguation for unsafe repair | satisfies | fail closed on contradictions; exact actions required for sensitive lease classes; unit tests; production |
| Brokerage execution contract parsing | [`pipeline/brokerage.go`](../../pipeline/brokerage.go), [`runtime/brokerage.go`](../../runtime/brokerage.go), [`turn/brokerage_stage.go`](../../turn/brokerage_stage.go) | model output and bounded text | parser + convergence loop | control flow: inspect/question/answer, plan seeding, governor awareness | model-authored pressure + governor ratification; lower ground than typed evidence | local argumentation preserves disagreement; not yet a general settled-belief demotion path | partial | fallback, adapt, reject, or stable-contract stop; unit tests; production but not authority-granting |
| Memory context governor | [`memory/context_governor.go`](../../memory/context_governor.go), [`memory/perception_budget.go`](../../memory/perception_budget.go) | free text + typed context requests | scorer/ruleset | perception and salience: lean/normal/deep/doctor recall and layer admission | heuristic judgment over memory and request text | typed challenge needed when recalled memory conflicts with current evidence or operator request | debt | conservative budget cap and suppression records; unit tests; production calibration needed |
| Evidence hydration selection | [`session/store_evidence.go`](../../session/store_evidence.go) | typed evidence metadata + query text | scorer/ruleset | perception and durable hydration trace | typed evidence metadata and scoped query | can provide decorrelated ground for challenges; missing evidence records trigger fallback | partial | record missing evidence and fallback use; do not cross session scope silently; unit tests + trajectory evals; production |
| Constitution and leakage checks | [`pipeline/constitution.go`](../../pipeline/constitution.go), [`turn/constitution_stage.go`](../../turn/constitution_stage.go) | visible text | recognizer/ruleset | presentation and repair control flow | visible candidate reply + delivered media/runtime facts | local repair only; not a general belief demotion path | partial | repair or fallback; recognized violations are explicit; unit tests; production presentation guard |
| Material floor and continuity presentation | [`pipeline/material.go`](../../pipeline/material.go), [`pipeline/fallback.go`](../../pipeline/fallback.go), [`pipeline/continuity_presentation.go`](../../pipeline/continuity_presentation.go) | model output + typed material floor | parser + ruleset | presentation salience: continuity, recovery, refusals, evidence visibility | model-authored floor parsed into typed packet; runtime facts can outrank it | needs contradiction challenge against later evidence, effect attempts, and current durable state | debt | quarantine legacy prose; apply typed visibility policy; unit tests; production |
| Continuation/recovery arbitration | [`runtime/recovery_candidate_arbitration.go`](../../runtime/recovery_candidate_arbitration.go), [`runtime/continuation_candidate_viability.go`](../../runtime/continuation_candidate_viability.go) | current request, working objective, operation state | recognizer + ruleset | temporal/control-flow authority: live, suppressed, explicit resume | current operator request + working objective + durable operation state | typed challenge when fresh intent contradicts recoverable history; operator explicit resume can override | partial | suppress stale candidates; require explicit resume on ambiguity; unit tests + trajectory evals; production |
| Re-entry recommendations | [`runtime/reentry_recommendation.go`](../../runtime/reentry_recommendation.go) | typed durable state + model-ranked candidate IDs | ruleset + ranker/model judgment | salience and presentation: next-step candidates | durable candidates; provider ranker is advisory model judgment | stale/contradicted candidates must be suppressible by deterministic rules | partial | deterministic fallback; provider may rank known IDs only; unit tests + dogfood; production/canary taste surface |
| Budget recovery scope | [`runtime/turn_budget_recovery.go`](../../runtime/turn_budget_recovery.go) | typed turn/run/operation state | ruleset | control flow: recover, park, ask, or block | interrupted run state + current operation; stale recovered context is low ground | typed challenge/disambiguation when current request and recoverable operation diverge | partial | fail closed when scope, objective, or authority are incompatible; unit tests + trajectories; production |
| Semantic memory source classification | [`memory/semantic_text.go`](../../memory/semantic_text.go), [`memory/semantic_promotion.go`](../../memory/semantic_promotion.go) | file paths, source text, semantic chunks | recognizer + ruleset | perception and durable memory categorization | heuristic classification over text/path; lower than evidence and current request | contradiction should flag rather than overwrite; promotion can abstain | partial | proposal/abstain before promotion where applicable; unit tests; production with heuristic edges |
| Evidence and metadata redaction | [`session/evidence_redaction.go`](../../session/evidence_redaction.go), [`durableagent/forensics.go`](../../durableagent/forensics.go) | raw text, command metadata, errors, child artifacts | recognizer/ruleset | perception and durable state: hydratable vs redacted evidence | raw artifact text classified by pattern rules | operator-only or non-hydratable classes can challenge ordinary hydration | partial | conservative masking for recognized secret shapes; not full DLP; unit tests with canaries; production safety membrane |
| Curiosity selection and pressure handoff | [`runtime/curiosity.go`](../../runtime/curiosity.go) | typed pressure, configured sources, source history | scorer/ranker/ruleset | salience and perception; read-only attention lane | advisory pressure + untrusted/fresh source observations | stranded handoff diagnostics; curiosity evidence remains advisory until corroborated | partial | skip ambiguous principal, backoff, diagnose stranded handoffs; unit tests; experimental/disabled by default |
| Telegram media and callback routing | [`telegram/client_media.go`](../../telegram/client_media.go), [`internal/telegramcommands`](../../internal/telegramcommands), [`internal/telegramdecision`](../../internal/telegramdecision) | external Telegram payloads | parser + ruleset | control flow and presentation: route media, callbacks, review decisions | external payload + durable callback/projection state | stale callbacks fail closed; operator disambiguation for ambiguous routing | satisfies | durable accepted/parked/terminal state; unit/integration tests; production |
| Outbound media classification | [`runtime/outbound_media.go`](../../runtime/outbound_media.go) | reply paths and MIME hints | recognizer/ruleset | presentation only | file containment + MIME/path hints | none as authority; unsafe/unsupported media is dropped | not applicable | drop unsafe/unsupported media; containment is structural; unit tests; production |
| Operation phase gates | [`runtime/operation_phase_gate.go`](../../runtime/operation_phase_gate.go), [`runtime/continuation_operation_contract.go`](../../runtime/continuation_operation_contract.go) | typed phase state plus reason codes | compiler/ruleset | authority narrowing and presentation: approval gate level/reason | durable phase state + authority reason codes | repair/block events become challenge evidence for stale or contradictory phase authority | partial | conservative gate on missing or contradictory phase authority; unit tests; production |
| Boundary attack and trajectory eval oracles | [`runtime/eval_boundary_attack.go`](../../runtime/eval_boundary_attack.go), [`runtime/eval_trajectory.go`](../../runtime/eval_trajectory.go) | synthetic/live transcripts and execution events | recognizer + oracle rules | evaluation evidence only | replayed trace/eval fixture, not runtime truth | eval replay can challenge release claims and classifier versions | satisfies | conservative findings; not runtime enforcement; deterministic and stochastic evals; release gate candidate |
| Model/role routing | [`docs/architecture/prompt-model-map.md`](prompt-model-map.md), runtime model-slot code | typed route/slot config | ruleset | cost/quality selection only | operator config + documented defaults | operator override and bakeoff replay | not applicable | fallback chain; unit tests + live bakeoffs; production |

## Review Checklist

When a PR adds or changes interpretation-like behavior, reviewers should ask:

1. What raw source is interpreted?
2. What judgment is produced, and does it carry enough provenance?
3. What consumer acts on the judgment?
4. What consequence class can the judgment affect?
5. What happens to unknown, partial, stale, or contradictory input?
6. Can a non-structural judgment become more consequential after conversion to
   a typed object?
7. What ground class supports the judgment?
8. What stronger ground could contradict or demote it?
9. Are correlated judgments being counted as independent evidence?
10. If a challenge is possible, what adjudicates it: typed rules, operator
    disambiguation, eval replay, or model-advisory text?
11. Which tests cover false positives, false negatives, partial parsing,
    stale-state composition, downstream consumer behavior, and later
    contradiction?
12. Is the judgment replayable under a new interpreter version?

Local mechanisms should stay local. The shared architecture work is to make the
judgment and challenge edges visible, replayable, and conservative enough that
Aphelion can challenge its own perceived reality later.
