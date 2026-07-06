# Interpretation, Judgment, and Dissent Surfaces

_Status: implemented kernel and current registry._
_Runtime enforcement: `interpretation.Service` is the central in-process
contract for consequential interpretation writes; `judgments` records selected
durable interpretations; `judgment_uses` records consequential uses for
shell/Codex execution, evidence
hydration, perception budget, adaptive recall, re-entry, recovery arbitration,
budget recovery, material floor, constitution repair, curiosity selection, and
brokerage control flow; challenge events and use reconciliation are persisted._
_Normative after: accepted implementation slices satisfy the consequential-use,
qualification, dependency, and reconciliation rules below._

Aphelion interprets constantly. It turns shell text into effect plans, model
output into brokerage contracts, memory candidates into context, continuity
records into visible status, and durable history into recovery choices.

Those judgments are necessary. The risk is that correlated interpretations can
become settled reality without a path for later evidence to challenge them.
Local conservatism does not compose into global conservatism when memory
admission, salience, brokerage, constitution checks, recovery arbitration, and
re-entry ranking all inherit the same false frame. A system can be careful about
a phantom.

This document reviews the interpretation surfaces that exist today, then names
the thin runtime kernel needed to make them safer:

```text
Judgment -> Consequential Use -> DependencyRefs -> Reconciliation
```

Ground is not a new truth store; it is the dependency, support, qualification,
and contradiction edges from a judgment or use to existing evidence, state, and
effect-attempt records. Challenge is not mandatory ceremony for every judgment;
it is the explicit path for disputed or uncertain cases. The common rule is
smaller:

```text
Record consequential uses.
Gate irreversible consequential uses.
Reconcile everything else.
```

`Dissent` is the system property: Aphelion can disagree with its own settled
interpretations. `Challenge` is the typed mechanism that carries one such
disagreement through recheck, adjudication, and possible demotion.

The registry at the end audits current surfaces against that model. It is a
current-state map over an implemented kernel. A row may claim `satisfies` only
when its consumer boundary is wired to a resolvable runtime call site; an
implemented helper with no live consumer is still an experiment, not an
architecture guarantee.

Research lineage and deliberate departures are recorded in
[`influences-and-departures.md`](influences-and-departures.md#truth-maintenance-argumentation-and-provenance).

## How To Read This Document

This document has two roles:

- current registry: the table maps interpretation-like surfaces that exist today
  and the seed surfaces that must stay visible for completeness;
- implemented kernel: `session.judgments` records selected durable
  interpretations, `session.judgment_uses` records selected consequential
  commitments, and `session.judgment_challenge_events` records append-only
  challenge/adjudication events;
- current registry: rows show whether each surface satisfies the current
  judgment/use/challenge boundary, is wired to a consumer, or is
  non-consequential.

Once the model is accepted through implementation experience, the stable
decisions should move into an ADR or a narrower normative architecture document.

## Current Shape

Aphelion already has an interpretation plane, but most of it exists as local
helper functions and package-specific policy. That locality is good. Shell
effects, memory admission, brokerage, redaction, recovery, and Telegram routing
each have domain details that should remain close to their owning code.

The current shape has real strengths:

- authority-sensitive execution increasingly fails closed on unknown, dynamic,
  or multi-authority shell plans;
- effect authorization is typed rather than inferred from proposal prose;
- recovery and re-entry use durable operation state instead of conversational
  summaries alone;
- evidence hydration is scoped and pull-oriented;
- output and metadata redaction make ordinary hydration safer than raw replay;
- Telegram callbacks and media routing use durable state machines instead of
  treating visible cards as authority.

The same shape also creates a systemic weakness. Each surface can be locally
careful while still inheriting a wrong premise from another surface. Recent
repairs show the pattern:

- shell classifiers that recognized one visible command could miss embedded or
  transitive effects, so the typed authority membrane saw less than Bash would
  execute;
- recovery arbitration could treat stale durable history as current intent until
  current-objective compatibility and explicit-resume checks were added;
- successful or uncertain mutations could be offered again until effect-attempt
  records and reconciliation made "may already have happened" durable;
- continuity and material-floor prose could become visible truth until typed
  continuity visibility separated backend recovery evidence from user-facing
  status;
- memory and recommendation surfaces can route attention toward a coherent but
  weakly grounded frame.

These failures were not caused by a lack of typed objects. In several cases the
wrong interpretation had already crossed into a typed record. The missing
property was a way to preserve the judgment's provenance, compare it with less
correlated ground later, and demote or verify it when stronger evidence
contradicted it.

That is the evidence for the architectural change. Aphelion should keep domain
interpreters local, but their consequential outputs need a common envelope and
explicit use records so later code can ask: what did Aphelion conclude, who used
that conclusion, what did it cause, which records did it depend on, and what
should happen when better evidence disagrees?

## Core Model

The runtime kernel has three commitments:

1. `Judgment`: what did Aphelion conclude, who concluded it, how complete was
   the interpretation, and what subject or claim does it describe?
2. `Consequential Use`: which consumer used that judgment, under which policy,
   to commit which consequence?
3. `Reconciliation`: what should happen if later evidence contradicts,
   supersedes, or weakens the judgment or the dependencies used to qualify it?

Dependency refs connect those commitments to existing evidence, state,
effect-attempt, operator-input, and provenance records. Challenges are only one
way a contradiction becomes explicit. The ordinary path is simpler: qualify a
judgment before consequential use, record the use, and reconcile the use if its
dependencies later fail.

The split follows the runtime failure modes above. A single "classified result"
is not enough because it cannot identify when a later consumer escalated that
conclusion into presentation, control flow, durable state, authority, execution,
or model perception. Keeping the use record separate lets Aphelion make a typed
object without treating the conversion itself as a trust upgrade.

```text
Evidence / durable state / effect attempts / operator input
        |
        | dependency refs support, qualify, or contradict
        v
     Judgment
        |
        | qualified under policy
        v
 Use / Commitment
        |
        | causes
        v
projection | state transition | effect attempt | diagnostic trace

Challenge targets judgments.
Adjudication changes future eligibility.
Reconciliation handles prior consequential uses.
```

This is not a call for a central semantic classifier. Domain mechanisms should
stay local. The implemented central surface is `interpretation.Service`: an
in-process contract that validates and persists consequential judgments, uses,
effect-attempt commitments, challenges, and decorrelation decisions over the
existing `session` ledger. It owns the write path, not the meaning of shell,
memory, brokerage, recovery, or path languages.

## Central Interpretation Service

`interpretation.Service` is the central service boundary for this kernel. It is
not an HTTP service, not a plugin registry, and not a universal classifier. It
has one purpose: consequential interpretation writes must pass through one
contract before they can become durable state, execution attempts, model-context
admission, recovery selection, or authority-looking presentation.

The service enforces cross-domain invariants that local classifiers should not
reimplement:

- complete judgments cannot carry unknown predicates;
- partial judgments must name typed unknowns;
- abstaining judgments still must cite dependency refs and source fault domains,
  because abstention is itself a consequential interpretation, not absence of
  provenance;
- consequential uses must cite judgments, dependency refs, policy, result, and
  qualification state;
- effect attempts and execution uses are committed through the same local
  transaction;
- irreversible uses can call the shared decorrelation qualification path before
  local commitment;
- challenge events and reconciliation updates remain append-only/durable.

Local packages still own local meaning. `commandeffect` understands shell
effect plans; `runtime` understands recovery and brokerage context; `memory`
understands recall; `pipeline` understands material and constitution parsing.
Those packages produce domain judgments, then call `interpretation.Service` to
make their consequential use visible and auditable.

Architecture checks reject production callers that bypass the service and write
raw judgment/use/effect-attempt-use records directly, except inside `session`
itself. Storage-owned structural paths such as evidence hydration remain
documented exceptions because they are already inside the durable ledger owner.

## Judgment

A judgment is any local interpretation that may later be used to affect
perception, salience, control flow, durable state, presentation, or authority.
It may come from a
compiler, parser, recognizer, scorer, ranker, ruleset, or model judgment.

Text may become a typed object, but typed output is not a trust upgrade by
itself. A brittle recognizer can emit a perfectly typed, completely wrong
object. Consequential judgments therefore need provenance, completeness,
failure semantics, stable identity, dependency versions, sensitivity policy, and
claim scope. Consequence belongs to a later use of the judgment, not to the
judgment producer.

Future consequential interpreters should converge on a typed judgment envelope.
The Go shape below is illustrative design notation, not a committed API:

```go
type Judgment[T any] struct {
    ID                 string
    Kind               string
    SchemaVersion      string
    Scope              ScopeRef
    PrincipalRef       string
    SubjectKey         string
    ClaimKey           string
    InterpreterID      string
    InterpreterVersion string
    InputRefs          []string
    InputHash          string
    Result             T
    Completeness       JudgmentCompleteness // complete, partial, abstain
    Unknowns           []UnknownPredicate
    Sensitivity        HydrationPolicy
    ContentHash        string
    AsOf               time.Time
    CreatedAt          time.Time
    ExpiresAt          time.Time
}
```

The useful question is not "how confident was the model?" It is:

- Was the full input language covered?
- Which regions were not understood?
- Is the result conservative for each possible consequence?
- Which downstream consumer used it?
- What evidence or state supports, qualifies, or contradicts it?
- Which stronger or less-correlated ground could demote it?
- Can it be replayed under a newer interpreter version?

`Completeness` and `Unknowns` are coupled: a complete judgment should have no
unknown predicates; a partial judgment should name them; an abstention should
explain why no result was safe to produce. Completeness is separate from
epistemic status, current eligibility, challenge status, lifecycle state,
expiration, and supersession.

## Dependencies

Aphelion does not have absolute ground truth. It can still preserve explicit
support, qualification, and contradiction relations over existing evidence and
state references:

- evidence A supports judgment J for claim C;
- evidence B contradicts judgment J for claim C;
- state version C qualifies judgment J until superseded.

These relations should reuse the evidence ledger, effect-attempt ledger, durable
state, and operator input records rather than create a second evidence ontology.
The same record can be strong support for one claim and weak support for
another: current operator input is strong support for present intent or consent,
but weak support for whether a remote deployment succeeded; an effect-attempt
record is strong support for "this may already have happened," but not for "this
completed successfully."

Dependency strength should therefore be computed as a profile, not a scalar
rank:

- authenticity;
- integrity;
- directness;
- freshness;
- scope compatibility;
- verification status;
- claim compatibility;
- source lineage;
- shared fault domains.

Dependency strength and source independence are separate axes. A strong record
from the same interpretive source may be useful as continuity, but it is weak
corroboration for dissent. A weaker but decorrelated observation can be more
useful for challenging a shared false frame.

Source independence should be derived from provenance, not self-declared by the
interpreter being checked. Relevant fault domains include source observation,
retrieval result, model call, model family, prompt/material floor, parser
version, interpreter version, memory summary, environment, and sensor. Two
judgments from the same parser over independent observations share parser risk
but not source risk; two different models reading the same stale summary remain
correlated through that summary.

The rule is that a judgment must not silently overrule a stronger contradiction
for the same claim, and a same-source judgment must not be treated as independent
corroboration.

## Consequential Use

A `Use` records the moment a downstream consumer relies on one or more judgments
to commit Aphelion to a consequence. This is where consequence belongs.

A shell effect judgment can be used by several consumers:

- proposal rendering, with a presentation consequence;
- authorization, with an authority consequence;
- effect-attempt persistence, with an execution consequence;
- diagnostics, with an observational consequence.

The judgment may be identical, but the consumer and consequence are different.
A harmless presentation judgment must not be reusable for dispatch without a
durable use record showing that it was qualified under the dispatch policy.

The Go shape below is illustrative design notation, not a committed API:

```go
type JudgmentUse struct {
    ID                 string
    JudgmentIDs        []string
    ConsumerID         string
    Consequence        ConsequenceClass
    PolicyRef          string
    GroundSnapshotHash string
    EligibilityVersion string
    ResultRef          string
    UsedAt             time.Time
}
```

For authority-sensitive state changes, a use record should be committed
atomically with the local state transition or local effect-attempt ledger entry
that records intent to act. It cannot be atomic with an external side effect
such as shell dispatch, Telegram delivery, repository publication, deployment,
or a remote API call. Qualification alone is not a reusable certificate. At use
time, the consumer must compare against the judgment lifecycle version,
dependency or ground snapshot, policy version, and operation or continuation
generation. If any changed before local commitment, the use fails and is
recomputed. Recompute must have a bounded retry or time budget; after that, the
surface should park, ask, or escalate rather than spin indefinitely. If the
action already crossed the dispatch boundary, later challenge triggers
reconciliation instead.

A use is consequential if it commits Aphelion to something another turn,
subsystem, or operator may rely on. Record and qualify uses that cross:

- durable state;
- external effects;
- authority, lease, grant, or approval decisions;
- operator-visible presentation of status, completion, recovery, next action, or
  authority;
- model context admission that can shape future recovery, completion, or
  authority-bearing reasoning.

Pre-commit decorrelation is required only for consequential uses that are
irreversible or externally costly. This includes external mutation, public or
operator-visible messages that cannot be unsent, deployment, repository
publication, and other actions where post-facto reconciliation can only
forward-correct. Reversible or low-cost consequential uses should still be
recorded and reconcilable, but they do not all need a synchronous decorrelation
gate.

## Challenge

Aphelion already has an argumentation layer in planning brokerage:

`face pressure -> governor ratification -> execution contract`

The face proposes how a turn should move. The governor ratifies, adapts, or
rejects that pressure. The negotiated artifact preserves both sides instead of
collapsing them into a single summary.

That pattern should be mapped onto settled judgments and their uses:

`settled judgment -> qualification -> use -> later challenge -> adjudication -> reconciliation`

The face or another local surface may initiate dissent, but the face should not
adjudicate it alone. Dissent is useful only when it can appeal to ground that is
less correlated with the challenged judgment: current durable state, immutable
evidence, effect-attempt records, fresh tool observations, timestamps, operation
lineage, or explicit operator input.

Challenge events are optional machinery for disputed or uncertain cases. The
common runtime path is qualification and reconciliation. The lifecycle has
separate mechanisms:

- qualification: checks whether a judgment is eligible for one specific use and
  consequence before commitment;
- invalidation or supersession: handles deterministic state changes such as a
  newer operation version, expired lease, replaced objective, or changed phase
  fingerprint;
- challenge and adjudication: handles conflicting ground, uncertain provenance,
  or disputed interpretation;
- reconciliation: handles consequences already produced by a judgment that was
  later demoted, contradicted, expired, or superseded.

The design object is an append-only challenge stream, not one mutable record
that contains both the initial disagreement and the final decision. The Go shape
below is illustrative design notation, not a committed API:

```go
type JudgmentChallengeEvent struct {
    ID               string
    ChallengeID      string
    EventKind        string // challenge_opened, ground_attached, adjudication_recorded, operational_response_recorded
    SubjectRef       string
    JudgmentID       string
    ChallengedBy     string
    Reason           string
    EvidenceRefs     []string
    Unknowns         []string
    Adjudicator      string // typed_ruleset, operator, eval_replay, model_advisory
    Disposition      EpistemicDisposition // supported, contradicted, unresolved
    Eligibility      FutureEligibility // eligible, suspended, superseded, expired
    Response         OperationalResponse // none, recompute, block, verify, retract, compensate, escalate
    DecisionEvidence []string
    CreatedAt        time.Time
}
```

The important architectural move is to name dissent as a first-class path.
Without it, every registry entry can become locally careful while the system
remains globally unable to challenge a coherent false premise.

Challenge decisions that affect authority, durable state, recovery priority, or
operator-visible completion must be adjudicated by typed rules over decorrelated
ground, by explicit operator disambiguation, or by replayable eval evidence. A
model judgment may initiate, summarize, or explain a challenge, but it must not
be the authority that demotes or upholds another model-shaped judgment.

Some challenge boundaries are mandatory. A model-authored or heuristic judgment
about to feed an authority-bearing, durable-state, recovery-selection, or
completion use must be qualified against at least one decorrelated ground source
before it acts, whether or not any surface "noticed" a conflict.

Decorrelated ground is not merely a second record. A judgment is not
independently corroborated by another artifact that shares the same model call,
material floor, memory summary, parser output, interpreter version, or upstream
evidence chain. The decorrelation decision is itself a judgment and should use
typed rules where it affects authority, durable state, recovery priority, or
completion.

The default must be direction-aware:

- use qualification: if decorrelated support cannot be established for the
  consequence, block or suspend the use;
- challenge admission: if the challenge has no minimum ground, reject,
  deduplicate, rate-limit, or ask for stronger evidence;
- demotion eligibility: if a serious contradiction exists but decorrelation
  cannot be established cleanly, do not silently uphold the settled judgment;
  lower eligibility, mark uncertain, or require verification according to the
  consequence.

In other words, "fail closed" for action prevents unsafe use. "Fail closed" for
challenge must not mean preserving a possible false frame merely because the
system cannot prove independence.

Challenges also have an admission policy. Untrusted content or a model should
not be able to create an epistemic denial of service by repeatedly manufacturing
unsupported contradictions. Consequential challenge paths need idempotency,
deduplication, minimum ground requirements, budgets or rate limits, rules for
whether pending challenges suspend future use, and operator escalation
thresholds.

Reconciliation is consequence-specific. A projection can be edited or retracted.
A recovery choice can be recomputed. A durable state transition can be
superseded. An external mutation, Telegram message, repository publication, or
deployment usually cannot be undone by demoting the judgment that authorized it.
For irreversible or externally visible effects, reconciliation means
forward-correction: verify the actual state, record the contradiction, notify or
escalate to the operator, compensate when a domain-specific compensation exists,
and prevent blind retry. It must never erase the prior use or the effect attempt
that may already have happened.

## Rules

- No non-structural judgment may silently acquire greater consequence by being
  converted into a typed object or reused by a higher-consequence consumer.
- Consequence belongs to `Use`: every consequential consumer must qualify the
  judgment under its own policy and record the commitment it made.
- A use is consequential when it commits Aphelion to something another turn,
  subsystem, or operator may rely on.
- Authority-sensitive uses must be committed atomically with the local state
  transition or local effect-attempt ledger entry they produce; external effects
  are handled through write-ahead attempts and later reconciliation.
- Irreversible or externally costly consequential uses require a pre-commit
  decorrelation gate; reversible consequential uses may rely on recording and
  reconciliation.
- More information may narrow a judgment. Missing information may only preserve
  or increase restrictions.
- A partial interpretation must expose its unknown regions or abstain before it
  reaches an authority-bearing consumer.
- Advisory judgments may route attention, but they may not create, widen, or
  repair authority.
- Presentation judgments may render an existing record, but they must not own
  the truth behind it.
- A settled judgment that later conflicts with stronger ground must have a
  demotion or verification path; demotion changes future eligibility and
  triggers reconciliation of prior uses rather than rewriting history.
- Model-authored or heuristic judgments that drive authority, durable state,
  recovery selection, or completion must be qualified against decorrelated ground
  before use.
- Challenge decisions that affect authority, durable state, recovery priority,
  or completion must be ruleset-, operator-, or replay-adjudicated, not decided
  by another unconstrained model judgment.
- Correlated judgments must not be counted as independent evidence.
- Eval oracles are release-gate evidence, not runtime enforcement.
- Qualification compare-and-swap must have bounded retry, timeout, or escalation
  behavior.
- Centralize consequential interpretation writes through `interpretation.Service`
  and centralize the judgment, consequential-use, dependency, reconciliation,
  registry, observability, and replay contracts; keep domain mechanisms local.

This does not make Aphelion impossible to fool. It makes consequential capture
visible, bounded, and recoverable.

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
| Use consequence | `presentation`, `salience`, `perception`, `control flow`, `durable state`, `authority narrowing`, `authority granting` |
| Failure semantics | `fail closed`, `conservative upper bound`, `abstain`, `defer`, `fallback`, `silent default` |
| Dependency profile | authenticity, integrity, directness, freshness, scope compatibility, verification status, claim compatibility, source lineage, shared fault domains |
| Dissent path | `none`, `local repair`, `typed challenge`, `verification required`, `operator disambiguation`, `eval replay` |
| Assurance | `examples`, `unit tests`, `adversarial corpus`, `fuzz/property tests`, `shadow evaluation`, `production calibration` |
| Lifecycle | `experimental`, `canary`, `production`, `deprecated` |
| Local compliance | `satisfies`, `not applicable` |
| Runtime wiring | `wired`, `not applicable` |
| Readiness tier | `registered`, `emitted`, `consumed`, `reconcilable`, `not_applicable` |
| End-to-end readiness | `ready`, `blocked by dependency`, `shadow only`, `unassessed` |
| Dependencies | stable surface IDs from `interpretation-surfaces.json` |

`projection` remains a state-surface truth class in
[`state-surfaces.md`](state-surfaces.md). Do not reuse it as an interpretation
maturity label.

## Current Local Compliance

Rows marked `satisfies` already have an appropriate challenge, verification,
or non-authority boundary for their consequence, and the machine-readable
registry must also mark them `wired`. Rows marked `not applicable` do not make
settled judgments consequential enough to require a demotion path and must be
`not_applicable` for wiring as well. Older drafts used `partial` and `debt`
while the registry was being seeded; the machine-readable registry rejects
those broad statuses now.

Implemented-but-unconsumed code must not be marked complete. The failure mode
this document is meant to prevent is a typed object claiming authority-class
consequence because it exists, while no consumer actually consults it.

The `Local compliance` column still does not prove that every downstream
pipeline is semantically perfect. It means the interpretation surface has a
registered owner, code anchor, consequence class, consumer boundary, consumer
anchor, and dissent adapter strong enough for its declared consequence. The
machine-readable readiness fields say how much of the judgment substrate is
actually proven:

- `registered`: the surface is named, owned, anchored, and has a declared
  consequence.
- `emitted`: production code records the declared judgment kind.
- `consumed`: production code records a `JudgmentUse` for the declared consumer.
- `reconcilable`: behavior tests exercise the challenge or reconciliation path
  named by the registry.
- `structural`: the surface is a typed compiler, validator, or deterministic
  policy boundary rather than a judgment producer; its anchors and tests carry
  the proof instead of `RecordJudgment` calls.

The registry is now backed by
[`interpretation-surfaces.json`](interpretation-surfaces.json). Architecture
checks verify stable surface IDs, owner fields, code anchors, challenge adapters,
status/wiring consistency, readiness tiers, and that every declared consumer
resolves to a non-test Go declaration rather than a leftover string literal.
For rows marked `emitted`, the gate scans production Go for the declared
`JudgmentInput.Kind`. For rows marked `consumed`, it scans production Go for the
declared `JudgmentUseInput.ConsumerID`. Rows marked `reconcilable` must name
behavior-test anchors that still resolve. Challenge adapters must use a
registered adapter token. The checks also reject broad `partial` or `debt`
states. The Markdown table below is the readable view; the JSON file is the
mechanical source of truth for review and CI.

This is a drift gate and a producer/consumer coverage gate, not a complete
semantic proof. A declaration anchor proves that the named adapter still exists;
the AST scan proves declared emitted/consumed tokens are present in production
call sites; behavior tests must still prove that the adapter is called on the
intended runtime path, emits the declared judgment, records the expected use,
and reconciles contradictions under real state.

## Current Registry

The registry describes edges:

`raw source -> interpreter -> judgment -> consumer -> consequence -> trace`

| Surface | Code anchors | Input trust | Mechanism | Use consequence | Dependency profile | Dissent path | Local compliance | Failure semantics / assurance |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Shell effect planner | [`commandeffect/effect.go`](../../commandeffect/effect.go), [`tool/exec_runtime.go`](../../tool/exec_runtime.go), [`session/store_judgments.go`](../../session/store_judgments.go) | bounded text/free shell string | parser + ruleset | authority narrowing, control flow, durable effect-attempt state | persisted shell-effect judgment + bounded text + typed plan; later effect attempt or fresh observation outranks it | verification required when outcome/effect evidence contradicts plan; challenge events can mark dependent uses pending reconciliation | satisfies | fail closed or conservative upper bound for unknown, dynamic, or multi-authority plans; exec records one durable plan judgment before proposal/auth/use; unit tests + adversarial corpus; production restricted-shell gate |
| Effect authorization | [`effectauth/effectauth.go`](../../effectauth/effectauth.go) | typed trusted envelope + command judgment | compiler/ruleset | authority narrowing/denial | current durable envelope, grant, lease, and shell effect plan judgment | local repair/block for invalid contracts; operator disambiguation for missing authority | satisfies | plan-aware authorization consumes the recorded effect plan in exec; command wrapper remains for compatibility; fail closed for invalid active envelopes and disallowed effects; unit tests; production authority membrane |
| Exec approval presentation | [`tool/exec_guard.go`](../../tool/exec_guard.go) | bounded text | recognizer | presentation and operator review salience | typed effect decision should outrank presentation text | none as authority; projection should be regenerated from typed decision | not applicable | defer to typed effect decisions; proposal text is not authority; unit tests; production presentation helper |
| Authority contract compilation | [`session/authority_contract.go`](../../session/authority_contract.go), [`session/authority_contract_compiler.go`](../../session/authority_contract_compiler.go), [`session/types_continuation.go`](../../session/types_continuation.go) | typed trusted fields plus bounded effect text | compiler with limited recognizers | authority narrowing/grant constraints | current durable proposal, phase, envelope, and exact action tokens | typed repair/block when prose and actions contradict; operator disambiguation for unsafe repair | satisfies | fail closed on contradictions; exact actions required for sensitive lease classes; unit tests; production |
| Dependency decorrelation adjudication | [`session/judgment_decorrelation.go`](../../session/judgment_decorrelation.go), [`tool/exec_runtime.go`](../../tool/exec_runtime.go) | judgment metadata, evidence refs, interpreter versions, lineage | ruleset | irreversible exec qualification for authority/execution use | compares source independence across model call, material floor, memory summary, parser output, interpreter version, and upstream evidence chain; direct exec support must cite a durable approved operator decision, and continuation support must cite an operator-approved lease | irreversible exec blocks when support is missing, unresolved, incomplete, or correlated; broader demotion adapters should reuse the same contract before claiming compliance | satisfies | deterministic rules reject missing provenance, unresolved upstream refs, shared fault domains, direct dependency refs, and transitive judgment dependencies; exec irreversible qualification consumes an operator-decision or operator-approved-continuation ground before recording a use; unit tests cover correlated, decorrelated, missing, incomplete, and transitive ground |
| Judgment use and commitment | [`session/types_judgment.go`](../../session/types_judgment.go), [`session/store_judgments.go`](../../session/store_judgments.go), [`session/types_judgment_use.go`](../../session/types_judgment_use.go), [`session/store_judgment_uses.go`](../../session/store_judgment_uses.go), [`tool/exec_runtime.go`](../../tool/exec_runtime.go), [`runtime/reentry_recommendation.go`](../../runtime/reentry_recommendation.go), [`runtime/recovery_candidate_arbitration.go`](../../runtime/recovery_candidate_arbitration.go), [`runtime/brokerage.go`](../../runtime/brokerage.go) | persisted judgment IDs, policy refs, dependency snapshots, operation/run generations | compiler/ruleset | records selected presentation, control-flow, model-context, recovery-selection, execution, and diagnostic commitments | persisted judgment plus dependency snapshot and invocation/run identity | append-only challenge events can locate dependent uses and mark them pending reconciliation | satisfies | insert-only immutable judgment and use commitments; exec dispatch writes judgment use and effect attempt before dispatch; irreversible exec requires approved proposal or active continuation ground; unit tests cover immutable replay, contradiction events, and use reconciliation |
| Brokerage execution contract parsing | [`pipeline/brokerage.go`](../../pipeline/brokerage.go), [`runtime/brokerage.go`](../../runtime/brokerage.go), [`turn/brokerage_stage.go`](../../turn/brokerage_stage.go) | model output and bounded text | parser + convergence loop | control flow: inspect/question/answer, plan seeding, governor awareness | model-authored pressure + governor ratification; lower ground than typed evidence | local argumentation preserves disagreement; final brokerage contract is recorded as a control-flow judgment/use | satisfies | fallback, adapt, reject, or stable-contract stop; durable control-flow judgment/use recorded after convergence; unit tests; production but not authority-granting |
| Memory context governor | [`memory/context_governor.go`](../../memory/context_governor.go), [`memory/perception_budget.go`](../../memory/perception_budget.go), [`runtime/interpretation_judgments.go`](../../runtime/interpretation_judgments.go) | free text + typed context requests | scorer/ruleset | perception and salience: lean/normal/deep/doctor recall and layer admission | heuristic judgment over memory and request text | typed challenge when recalled memory conflicts with current evidence or operator request | satisfies | perception budget and adaptive recall admissions record model-context judgments/uses; conservative budget cap and suppression records; unit tests; production |
| Evidence hydration selection | [`session/store_evidence.go`](../../session/store_evidence.go) | typed evidence metadata + query text | scorer/ruleset | perception and durable hydration trace | persisted hydration-selection judgment + typed evidence metadata and scoped query | can provide decorrelated ground for challenges; missing evidence records produce partial/abstain judgments | satisfies | records selected/missing evidence judgment before model-context use; do not cross session scope silently; unit tests + trajectory evals; production |
| Constitution and leakage checks | [`pipeline/constitution.go`](../../pipeline/constitution.go), [`turn/constitution_stage.go`](../../turn/constitution_stage.go), [`runtime/constitution_runtime.go`](../../runtime/constitution_runtime.go), [`runtime/interpretation_judgments.go`](../../runtime/interpretation_judgments.go) | visible text | recognizer/ruleset | presentation and repair control flow | visible candidate reply + delivered media/runtime facts | local repair and typed violation judgment; dependent presentation use can be challenged by later evidence | satisfies | repair or fallback; recognized violations are explicit; constitution violations record presentation judgments/uses; unit tests; production presentation guard |
| Material floor and continuity presentation | [`pipeline/material.go`](../../pipeline/material.go), [`pipeline/fallback.go`](../../pipeline/fallback.go), [`pipeline/continuity_presentation.go`](../../pipeline/continuity_presentation.go), [`runtime/interpretation_judgments.go`](../../runtime/interpretation_judgments.go) | model output + typed material floor | parser + ruleset | presentation salience: continuity, recovery, refusals, evidence visibility | model-authored floor parsed into typed packet; runtime facts can outrank it | contradiction challenge against later evidence, effect attempts, and current durable state | satisfies | structured and unstructured material floors record presentation judgments/uses with partial unknowns when needed; unit tests; production |
| Continuation/recovery arbitration | [`runtime/recovery_candidate_arbitration.go`](../../runtime/recovery_candidate_arbitration.go), [`runtime/continuation_candidate_viability.go`](../../runtime/continuation_candidate_viability.go) | current request, working objective, operation state | recognizer + ruleset | temporal/control-flow authority: live, suppressed, explicit resume | current operator request + working objective + durable operation state | typed challenge when fresh intent contradicts recoverable history; operator explicit resume can override | satisfies | stale-candidate suppressions record recovery arbitration judgments and recovery-selection uses; viability remains bounded by deterministic events and current-intent challenge; unit tests + trajectory evals; production |
| Re-entry recommendations | [`runtime/reentry_recommendation.go`](../../runtime/reentry_recommendation.go) | typed durable state + model-ranked candidate IDs | ruleset + ranker/model judgment | salience and presentation: next-step candidates | persisted recommendation-selection judgment + durable candidates; provider ranker is advisory model judgment | stale/contradicted candidates are suppressible by deterministic rules | satisfies | deterministic fallback; provider may rank known IDs only; presentation use references durable selection judgment; unit tests + dogfood; production/canary taste surface |
| Budget recovery scope | [`runtime/turn_budget_recovery.go`](../../runtime/turn_budget_recovery.go), [`runtime/interpretation_judgments.go`](../../runtime/interpretation_judgments.go) | typed turn/run/operation state | ruleset | control flow: recover, park, ask, or block | interrupted run state + current operation; stale recovered context is low ground | typed challenge/disambiguation when current request and recoverable operation diverge | satisfies | every budget recovery scope return commits a recovery-selection judgment/use; stale-operation suppression also records arbitration; unit tests + trajectories; production |
| Semantic memory source classification | [`memory/semantic_text.go`](../../memory/semantic_text.go), [`memory/semantic_promotion.go`](../../memory/semantic_promotion.go) | file paths, source text, semantic chunks | recognizer + ruleset | perception and durable memory categorization | heuristic classification over text/path; lower than evidence and current request | contradiction flags rather than overwrites; promotion can abstain | satisfies | imports classify deterministically; promotion requires approved imports and can abstain; unit tests; production with heuristic edges |
| Evidence and metadata redaction | [`session/evidence_redaction.go`](../../session/evidence_redaction.go), [`durableagent/forensics.go`](../../durableagent/forensics.go) | raw text, command metadata, errors, child artifacts | recognizer/ruleset | perception and durable state: hydratable vs redacted evidence | raw artifact text classified by pattern rules | operator-only or non-hydratable classes challenge ordinary hydration | satisfies | conservative masking for recognized secret shapes; credential-bearing output becomes non-hydratable; unit tests with canaries; production safety membrane |
| Curiosity selection and pressure handoff | [`runtime/curiosity.go`](../../runtime/curiosity.go), [`runtime/interpretation_judgments.go`](../../runtime/interpretation_judgments.go) | typed pressure, configured sources, source history | scorer/ranker/ruleset | salience and perception; read-only attention lane | advisory pressure + untrusted/fresh source observations | stranded handoff diagnostics; curiosity evidence remains advisory until corroborated | satisfies | selected candidates record salience judgments/uses; skip ambiguous principal, backoff, diagnose stranded handoffs; unit tests; experimental/disabled by default |
| Telegram media and callback routing | [`telegram/client_media.go`](../../telegram/client_media.go), [`internal/telegramcommands`](../../internal/telegramcommands), [`internal/telegramdecision`](../../internal/telegramdecision) | external Telegram payloads | parser + ruleset | control flow and presentation: route media, callbacks, review decisions | external payload + durable callback/projection state | stale callbacks fail closed; operator disambiguation for ambiguous routing | satisfies | durable accepted/parked/terminal state; unit/integration tests; production |
| Outbound media classification | [`runtime/outbound_media.go`](../../runtime/outbound_media.go) | reply paths and MIME hints | recognizer/ruleset | presentation only | file containment + MIME/path hints | none as authority; unsafe/unsupported media is dropped | not applicable | drop unsafe/unsupported media; containment is structural; unit tests; production |
| Operation phase gates | [`runtime/operation_phase_gate.go`](../../runtime/operation_phase_gate.go), [`runtime/continuation_operation_contract.go`](../../runtime/continuation_operation_contract.go) | typed phase state plus reason codes | compiler/ruleset | authority narrowing and presentation: approval gate level/reason | durable phase state + authority reason codes | repair/block events become challenge evidence for stale or contradictory phase authority | satisfies | conservative gate on missing or contradictory phase authority; unit tests; production |
| Boundary attack, trajectory, and scenario-generator eval oracles | [`runtime/eval_boundary_attack.go`](../../runtime/eval_boundary_attack.go), [`runtime/eval_trajectory.go`](../../runtime/eval_trajectory.go) | synthetic/live transcripts, scenario generator output, execution events | recognizer + oracle rules | release-gate evidence only | replayed trace/eval fixture, not runtime truth; generator assumptions are lower ground than external benchmark traces and incidents | eval replay challenges release claims and classifier versions; generator blind spots require incident-derived fixtures | satisfies | conservative findings; not runtime enforcement; deterministic and stochastic evals; release gate candidate |
| Model/role routing | [`docs/architecture/prompt-model-map.md`](prompt-model-map.md), runtime model-slot code | typed route/slot config | ruleset | cost/quality selection only | operator config + documented defaults | operator override and bakeoff replay | not applicable | fallback chain; unit tests + live bakeoffs; production |

End-to-end readiness examples:

| Slice | Local status | End-to-end readiness | Dependency note |
| --- | --- | --- | --- |
| Shell execution | shell planner, effect authorization, and exec use commitment are locally bounded | ready | exec dispatch records one immutable shell-effect judgment, one local use, and one effect attempt before dispatch, with invocation identity |
| Recovery/re-entry | arbitration and recommendation surfaces are locally bounded | ready | re-entry presentation, stale recovery suppressions, and budget-recovery scopes record recovery-selection judgments and uses |
| Evidence/perception | scoped hydration, perception budget, and adaptive recall are locally bounded | ready | hydration, perception-budget, and adaptive-recall admissions record selected/missing evidence or salience judgments before model-context admission uses |
| Presentation | material floor and constitution checks are locally bounded | ready | material-floor interpretation and constitution repair/fallback decisions record presentation judgments and uses |

## Implementation Payoff

If the consequential-use architecture is implemented, it should simplify
several parts of the current system. The goal is not to add a new central
classifier. The goal is to replace repeated local substitutes for provenance,
completeness, use tracking, and contradiction handling with one contract that local
interpreters can emit and consumers can inspect.

Before:

```text
                 command text
                      |
        +-------------+-------------+
        |             |             |
 shell planner   proposal text   effect auth
        |             |             |
  local labels   local matcher   reparses command
        |             |             |
        +------+------+-------------+
               |
        effect attempt
               |
        outcome evidence
```

After:

```text
                 command text
                      |
              effect judgment
       {effects, unknowns, ground refs, version}
                      |
        +-------------+-------------+
        |             |             |
 proposal view   effect auth   attempt ledger
        |             |             |
 projection use  authority use execution use
        |             |             |
        +-------------+-------------+
                      |
      effect attempt / projection / diagnostic
                      |
    outcome evidence challenges judgment and reconciles uses
```

The same shape applies outside shell execution. Today, recovery, memory,
continuity, and recommendations each carry local fragments of "why this
matters" and "what could contradict it." The target architecture turns those
fragments into reusable judgment records.

```text
Before:

operator request
      |
working objective      old operation       memory recall
      |                     |                   |
local matcher          local viability      local scorer
      |                     |                   |
      +---------- competing conclusions --------+
                         |
                 recovery or next step

After:

operator request     old operation     memory recall
      |                    |                |
 intent judgment   operation judgment  salience judgment
      |                    |                |
      +--------- qualification/use ---------+
                         |
              recovery commitment recorded
                         |
          current, stale, ambiguous, or verify
```

Concrete before/after examples:

| Area | Before | After |
| --- | --- | --- |
| Shell effects | Planner, proposal rendering, authorization, and attempt persistence each inspect command text. | One effect judgment carries effects, unknowns, ground, and version; each consumer records its own use under presentation, authority, or execution policy. |
| Recovery | Budget recovery, re-entry, and continuation viability each decide stale-versus-current with local matchers. | A shared consequential-use contract plus local adapters compares current intent, operation lineage, freshness, and explicit resume support, then records the chosen recovery use. |
| Continuity | Material-floor or recovery prose can require presentation-specific quarantine. | Continuity arrives as a visibility judgment with evidence refs and a contradiction path. |
| Memory | Admission and hydration explain little about why recalled material entered context. | Hydration records the salience judgment, the perception use, dependency profile, scope, and stronger facts that could demote it. |
| Redaction | Output, command metadata, errors, and artifacts need separate safety membranes. | Evidence safety becomes a judgment attached to every persisted or hydratable artifact. |
| Evals | A trajectory can fail without showing which interpretation moved the system. | Replay compares stored judgments and uses across interpreter versions and reports consequence changes. |

Concrete cleanup targets:

- Shell planning, proposal rendering, effect authorization, and effect-attempt
  persistence can consume the same effect judgment and record separate uses
  instead of reparsing command text at each layer.
- Recovery arbitration, budget recovery, and re-entry recommendations can share
  one stale-versus-current consequential-use contract and reusable typed predicates
  instead of carrying separate token overlap, freshness, and explicit-resume
  rules.
- Material-floor, continuity, and constitution repair paths can stop converting
  prose into special cases and instead attach visibility and contradiction
  decisions to typed judgments.
- Evidence hydration and memory admission can expose why a recalled object was
  admitted, which perception use consumed it, what ground it rests on, and which
  current facts could demote it.
- Eval oracles can replay stored judgments against newer interpreter versions
  and report changed uses and consequences, rather than only reporting pass/fail
  outputs.
- Operator-facing diagnostics can answer "which judgment caused this?" and
  "which use committed it?" and "what could contradict it?" without
  reconstructing the answer from scattered events.

This is also a deletion plan. Successful implementation should remove
duplicated matcher logic, ad hoc reason strings, presentation-driven authority
checks, and one-off "stale versus current" policies where a shared judgment or
challenge record can carry the same fact more truthfully.

## Operator Experience

The payoff should be visible to the operator. The user should not experience the
architecture as more ceremony. They should experience it as fewer stale prompts,
clearer blockers, better recovery, and more precise explanations when Aphelion
refuses to act.

Golden path: approve bounded work.

```text
Before:

User: bundle artifacts, commit, and push
Aphelion: proposes a phase
User: approves
Aphelion: asks for more approval or stalls on a local mismatch
User: cannot tell which fact blocked the run

After:

User: bundle artifacts, commit, and push
Aphelion: proposes one typed plan with repo publication authority
User: approves
Aphelion: records authority and execution uses, then effect attempts before dispatch
Aphelion: completes or asks for verification with named evidence
User sees: result, or a blocker tied to the exact judgment that failed
```

Why better: the proposal, authorization, attempt ledger, and completion check
come from the same judgment chain, while use records separate presentation,
authority, and execution consequences. The operator no longer has to infer
whether a visible card, a prose objective, and the typed authority envelope
disagree.

Golden path: recover after interruption.

```text
Before:

Provider budget ends
Recovery scans durable history
An older active operation can look more recoverable than the current request
The next turn resumes yesterday's work or asks a generic "continue?"

After:

Provider budget ends
Recovery emits a candidate judgment
Current request and working objective provide stronger ground
Qualification records why the recovery use is compatible or blocked
The next turn resumes compatible work or asks a typed disambiguation question
```

Why better: durable history remains evidence, not authority. The operator sees a
recovery path only when it is compatible with current intent or explicitly
selected.

Golden path: hydrate evidence under pressure.

```text
Before:

Long history accumulates
Summary or memory recall enters context because it seems relevant
The model inherits a stale premise and repeats it confidently

After:

Long history accumulates
Hydration emits a salience judgment with ground and scope
Aphelion records the perception use before adding it to context
Contradictory current evidence can challenge the recalled premise
Aphelion either uses the evidence, marks it uncertain, or asks for verification
```

Why better: memory helps without silently becoming equal to current operator
input, verified execution, or immutable evidence.

Edge case: stale approval card.

```text
Before:

Old card remains visible
Operator taps it after the operation moved on
Callback path may reject the UI event, but related authority can remain unclear

After:

Callback carries projection and authority fingerprints
Current durable state challenges the stale projection
Aphelion terminalizes the callback and points to the superseding operation
```

Expected experience: "That approval is stale; the current operation is X." The
system should not ask the operator to guess whether the button still matters.

Edge case: correlated false premise.

```text
Before:

Memory summary says phase F is done
Recommendation ranker suggests phase G
Recovery sees phase G as next
All three agree because they inherited the same false summary

After:

Phase-completion judgment cites model summary ground
Effect-attempt ledger has no verified completion
Qualification blocks the phase-G authority use and opens a challenge
Aphelion asks to verify phase F or marks phase G blocked
```

Expected experience: Aphelion does not sound less confident merely because it is
confused. It names the missing stronger ground.

Edge case: ambiguous operator intent.

```text
Before:

User: do not resume the scout, finish the PDF
Token overlap sees "scout" and keeps the scout operation live

After:

Intent judgment records negation and target objective
Recovery candidate judgment is challenged by current operator input
Scout is suppressed; PDF remains current
```

Expected experience: negation and recency are not taste signals. They are ground
for suppressing the wrong continuation.

Edge case: unsafe command interpretation.

```text
Before:

Command text has one visible safe label but hidden execution behavior
Proposal describes the visible label
The shell executes the hidden behavior

After:

Effect judgment exposes unknown, dynamic, or multi-authority regions
Authorization operates on the conservative upper bound
Aphelion rejects or asks for a split typed operation
```

Expected experience: the operator sees a refusal or split request that explains
which part could not be bounded, not a misleading approval card.

These flows are the user-facing test of the design. A correct implementation
should reduce generic "continue?" prompts, stale approval cards, unexplained
silence, repeated plans, and confident answers grounded only in inherited model
state. It should replace them with specific next actions: complete, verify,
ask, suppress, demote, or block.

## Rollout Sequence

Do not turn the kernel into a central classifier. The implemented kernel records
durable `Judgment` rows, immutable `JudgmentUse` commitments, and append-only
challenge events for selected high-consequence surfaces. Further vertical
slices should keep teaching the abstraction what it actually needs:

1. Registry and commitment traces. Keep stable surface IDs and record durable
   `judgments` plus `judgment_uses` where consumers cross a consequence
   boundary.
2. Shell effect interpretation. The planner creates one immutable shell-effect
   judgment; exec authorization and effect-attempt persistence consume its ID
   and hash. Remaining shell work is to remove presentation-only reparsing from
   proposal rendering and diagnostics.
3. Recovery arbitration. Candidate compatibility and current-intent judgments
   receive the same envelope, but a different domain policy. This tests
   staleness, supersession, correlation, and challenge without shell semantics.
4. Challenge and adjudication events. Introduce qualification, invalidation,
   challenge admission, adjudication, and dependency propagation.
5. Reconciliation. Add consequence-specific handlers for projections, current
   state, recovery choices, and external effects.
6. Memory and hydration. Run in shadow mode first. These surfaces are high
   volume and can generate challenge storms, so calibrate precision and operator
   burden before enforcement.

Persist only consequential judgments, consequential uses, or records required
for replay. Local intermediate calculations should remain local; otherwise the
interpretation plane becomes a log of every fleeting calculation rather than a
governed commitment surface.

Persist `Use` records only when a consumer commits a consequence that future code
may need to audit, challenge, or reconcile: authority, durable state, execution,
recovery selection, completion, evidence hydration, or operator-visible
projection. Low-consequence scoring, ranking, formatting, and local helper
calculations can remain unpersisted when they do not cross a consequence
boundary. The registry includes some `not applicable` rows to preserve this
boundary; implementation should not make every helper pay the full judgment/use
cost merely for symmetry.

## Evaluation Model

Prefer adapting existing benchmarks before inventing a new benchmark. Suites
such as MemoryArena, EMemBench, EvoMemBench, WebArena, OSWorld, tau-bench, and
SWE-bench-style tasks provide realism, comparability, and long-horizon pressure
that a local toy environment will not capture on its own.

Those benchmarks should be instrumented with Aphelion-specific traces:

- which judgments were emitted;
- which ground each judgment cited;
- whether the ground was decorrelated from the challenged judgment;
- which consumers acted on each judgment;
- whether a challenge fired, was skipped, or was adjudicated;
- whether a judgment was upheld, demoted, marked uncertain, or verified.

Existing benchmarks are not enough by themselves. They usually measure task
completion, memory utility, tool use, or environment success. They do not
directly assert Aphelion-specific invariants such as "correlated judgments are
not independent ground" or "fresh operator intent outranks stale durable
history." For those invariants, Aphelion also needs a small deterministic
judgment game with a hidden world, several imperfect interpreters, and a limited
set of actions.

One minimal game:

1. The world contains hidden facts, observations, a current objective, a stale
   objective, a small evidence ledger, one possible mutation, one visible
   projection, and one operator message.
2. Interpreters emit judgments about intent, relevance, authority, completion,
   evidence salience, and projection visibility. Some interpreters share a
   correlated false premise; others read decorrelated ground.
3. Consumers qualify judgments for uses: proposal rendering, recovery choice,
   evidence hydration, approval request, dispatch, or diagnostic trace.
4. The agent can act by asking, verifying, suppressing, recovering, hydrating,
   proposing approval, executing, demoting a prior judgment, or reconciling a
   prior use.
5. The transition system records every judgment consumed, every use committed,
   every challenge triggered, every adjudication, every reconciliation response,
   every effect, and every user-visible projection.
6. The scenario generator enumerates combinations of stale/current intent,
   terminal/active operations, missing evidence, contradictory evidence,
   correlated model summaries, and valid or invalid authority envelopes.

The expected behavior is not only task completion. The gate should measure:

- false authority: an action executes from a judgment that lacked sufficient
  ground, skipped mandatory qualification, or lacked a use record;
- false suppression: current work is suppressed by stale or correlated history;
- demotion latency: how many steps pass before contradictory ground demotes a
  bad judgment;
- challenge precision and recall: whether challenges fire when required without
  blocking unrelated local judgments;
- recovery quality: whether the system resumes compatible work, asks on
  ambiguity, and refuses stale authority;
- context fidelity: whether hydration uses the evidence needed to resolve the
  conflict rather than reinforcing the same correlated premise;
- operator burden: how many questions or approvals are needed to reach the
  desired outcome or a correct blocker.

Hard safety properties should be explicit:

- no high-consequence use without qualification;
- no correlated supports counted as independent ground;
- no suspended, expired, or superseded judgment used for a new consequence;
- no model-only adjudication of authority-bearing conflict;
- no prior effect attempt erased by later demotion;
- no uncertain mutation automatically retried.

Liveness properties should be explicit too:

- decorrelated contradiction eventually changes eligibility;
- inconclusive verification eventually yields another bounded surface;
- a false challenge cannot stall the system forever;
- repeated qualification compare-and-swap conflicts eventually park, ask, or
  escalate instead of recomputing forever;
- recovery converges on current compatible intent;
- operator burden remains bounded.

The game should include adversarial fixtures, but it should also enumerate the
benign cases where no challenge is necessary. That lets implementation changes
optimize for fewer steps to correct outcomes without hiding the blocker and
degradation cases this design is meant to expose.

The deterministic game is not ground truth. Its scenario generator is another
interpretation surface, built by the same project that builds the runtime, and
can inherit the same blind spots. Treat it as release-gate evidence, not runtime
authority. Its assumptions should be versioned, replayable, and challenged by:

- incident-derived fixtures from live failures;
- adapters for external benchmarks and trajectories;
- mutation tests that perturb stale/current state, dependency profiles, and
  interpreter outputs;
- held-out scenarios reviewed separately from the implementation under test.

The evaluation stack should be layered:

- benchmark adapters: first choice for ecological validity and comparison
  against existing agent and memory work;
- deterministic game: release-time gate for Aphelion-specific invariants,
  transition coverage, and regression debugging, not runtime enforcement;
- stochastic replay: provider-backed rollouts over longer histories, noisy
  summaries, context pressure, and model disagreement to measure whether
  challenges fire too often, too late, or in the wrong place;
- shadow production metrics: non-authorizing telemetry for challenge rate,
  demotion latency, false challenge suppressions, hydration hit rate, stale
  recovery suppressions, operator burden, and model disagreement.

Existing benchmarks should provide realism and external pressure. The
deterministic harness should provide proof-shaped regression coverage for the
architecture's safety laws, while remaining honest that its generator is another
audited judgment surface. Stochastic replay and shadow metrics should calibrate
judgment quality, taste, cost, and operator burden under realistic uncertainty.

Scoring should be lexicographic:

1. hard safety violations, which must remain zero;
2. epistemic and liveness quality, including false suppression, challenge recall,
   and demotion latency;
3. efficiency, including tokens, steps, hydration calls, and operator questions.

Do not use a weighted aggregate that lets lower cost compensate for false
authority or erased side-effect history.

## Review Checklist

When a PR adds or changes interpretation-like behavior, reviewers should ask:

1. What raw source is interpreted?
2. What judgment is produced, and does it carry enough provenance?
3. What ground supports, qualifies, or contradicts the judgment?
4. What consumer uses the judgment, under which policy, to commit which
   consequence?
5. If the use commits durable state or execution intent, is it recorded
   atomically with the local state transition or local effect-attempt ledger
   entry?
6. What happens to unknown, partial, stale, or contradictory input?
7. Can a non-structural judgment become more consequential after conversion to
   a typed object?
8. What stronger or less-correlated ground could contradict or demote it?
9. Are correlated judgments being counted as independent evidence?
10. If a challenge is possible, what adjudicates it: typed rules, operator
    disambiguation, eval replay, or model-advisory text?
11. Is the decorrelation decision itself registered, tested, and replayable?
12. What reconciles prior uses if the judgment is later demoted, contradicted,
    expired, or superseded?
13. What is the retry, timeout, or escalation bound for qualification
    compare-and-swap conflicts?
14. Which tests cover false positives, false negatives, partial parsing,
    stale-state composition, downstream consumer behavior, and later
    contradiction?
15. Is the judgment replayable under a new interpreter version?

Local mechanisms should stay local. The shared architecture work is to make the
judgment, use, and challenge edges visible, replayable, and conservative enough
that Aphelion can challenge its own perceived reality later.
