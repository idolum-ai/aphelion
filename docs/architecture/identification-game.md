# The Identification Game

_Status: draft architecture direction._

Aphelion's long-running plans should discover authority the way a roguelike
player identifies items: by learning the current run's resource ontology under
a fresh shuffle, then using only the labels proven in that run.

The goal is not to make approval more theatrical. The goal is to reduce
operator interruptions for long plans while preserving the hard invariant that
unidentified authority never executes.

## Core Metaphor

Two game mechanics describe the boundary:

- Snake supplies the topology. Growth is the hazard. A long run carries a tail
  of grants, leases, bundles, evidence, and prior decisions. If the tail is not
  shed when its reason ends, later steps can collide with stale authority.
- Roguelikes supply the epistemics. Items are identified per run. The same
  visible shape can mean something different under a new session, plan version,
  or grant state. The player learns by use, metadata, identify scrolls, and
  batch identification.

The Aphelion mapping is:

| Roguelike mechanic | Aphelion counterpart |
| --- | --- |
| Unidentified item | Authority shape a plan will need but has not reached yet |
| Per-run shuffle | Session-bound contract identity |
| Identify by use | Attempt a tool call and receive a typed blocker |
| Price identification | Static plan/grant analysis before execution |
| Scroll of identify | Operator review card |
| Batch identification | Authority bundle |
| Special gift | Small plan-start authority loadout |
| Full-column bonus | Reward for identifying diverse authority classes |
| Persistent artifact | Bundle with hash-bound expiry |
| Tail | Live accumulated authority behind a plan |
| Tail collision | Confused-deputy reuse of stale authority |
| Shedding | One-time leases, generations, expiry, terminalization |

This is more than a metaphor. It is a design rule for authority-sensitive
planning: authority labels are run-local, evidence-backed, and perishable.

## Strategic Inversion

Systems that cannot safely collide with authority boundaries usually identify
permissions in advance. They precompute the approvals the plan may need, obtain
them, and then execute.

Aphelion has a different economic model because unsatisfied authority fails
closed. A tool call that lacks authority does not execute the underlying effect.
It commits a typed blocker, a recovery contract, a next action, and a review
surface. The failed attempt is therefore a harmless identification event:

```text
attempt
  -> authority boundary says "missing"
  -> typed blocker
  -> compiled recovery contract
  -> review card
  -> replayable retry
```

So Aphelion should prefer discovery over broad pre-granting:

- Codex-like systems compile a menu by simulating authority collisions up front.
- Aphelion can compile the menu by having real collisions that execute nothing.

The risk moves from "over-granted execution" to "operator interruption cost."
This document describes how to drive that cost down without letting preemptive
over-granting back into the design.

### Bounded Preemption

Discovery does not mean starting with nothing. Serpentes lets the player choose
a special gift before the run; the full version permits a second gift. That is
the honest form of preemption: a small fixed loadout carried into a shuffled
field, not a pre-identified map of the whole field.

Aphelion should model that distinction directly. At plan start, the operator may
approve a small plan loadout: one or two named bundles, leases, or standing
capability shapes with explicit scope and expiry. Everything beyond the loadout
is discovered by collision, static analysis, operator review, or frontier-bound
lookahead.

The count limit matters. It turns preemption from a moral restraint into a
runtime rule:

- loadout slots are few and visible;
- each slot is named, hash-bound, and expiring;
- plan revision invalidates or revalidates the loadout;
- unused loadout authority contributes to over-grant mass;
- execution still requires point-of-use validation.

In menu projection, a loadout token is executable only when joined against live
authority state. A caller may pass pre-verified live loadout slots into the pure
menu compiler, but an unverified loadout remains one approval away. The word
`executable` should never mean "the loadout was mentioned"; it means the
corresponding grant, lease, or bundle is live at projection time.

This gives operators a practical way to say "start with these known tools" while
preserving the design rule that the run must identify the rest of its authority
ontology honestly.

## Ralph Loop As Run

A Ralph loop is a plan/execute/verify machine that advances only after durable
evidence proves the previous step completed. In this model it is the run.

- The dungeon is the plan: a partially ordered set of steps.
- The shuffle seed is the session, plus plan identity and plan version.
- The evidence gate is the durable state that proves a step completed.
- The tail is every live grant, lease, bundle, and speculative approval the run
  has accumulated.

The run's authority question is:

> What authority shapes has this plan identified, which properties are known,
> which observations produced those labels, and are they still live for the same
> session, plan version, and step?

## Identification Ledger

The missing architecture concept is a per-plan identification ledger. The ledger
is a canonical surface for discovered authority shapes inside one run.

Draft shape:

```go
type IdentificationLedgerEntry struct {
    PlanID      string
    PlanVersion string
    SessionID   string
    StepRef     string

    ShapeHash string // reusable authority shape, not request/contract instance
    LabelRef  string // contract/bundle/grant ref, or empty while partial
    Status    string // unidentified | partial | proposed | approved | consumed | expired | invalidated
    ExpiresAt time.Time

    CreatedAt time.Time
    UpdatedAt time.Time
}

type IdentificationLedgerObservation struct {
    EntryID string

    Method      string // collision | static | lookahead | operator
    Property    string // approval_class | resource | timeout | retryability | bundle_fit | ...
    Value       string
    EvidenceRef string
    ExpiresAt   time.Time

    ActorKind      string
    ActorPrincipal string
    ActorAction    string

    ObservedAt time.Time
}
```

The ledger's identity terms are contract terms. New writes must bind at least:

- `plan_id`
- `plan_version`
- `session_id`
- `step_ref`
- `shape_hash`

Changing any of those terms changes the meaning of the identification. A revised
plan is a partial reshuffle. Existing entries can be revalidated, but they must
not silently carry over.

`shape_hash` is deliberately coarser than a continuation-recovery contract hash.
It is the reusable authority-shape identity: lease class, tool, action, and
resource class. Contract hash, request instance, subject ref, and session-bound
retry details belong in labels and observations. Otherwise the ledger fragments
one authority shape into one row per collision and loses the cumulative
knowledge it exists to preserve. When the same step and shape surface a
different exact contract, the ledger should keep the shape relationship while
creating a new collision generation; the scalar label must not be silently
rewritten to point at a different approval object.

### Graduated Identification

Identification is not binary. The first collision may reveal only the approval
class; a later attempt may reveal timeout behavior; static analysis may add
resource shape; operator review may attach bundle compatibility; execution may
consume or invalidate the label.

The entry is the stable subject. Observations are the provenance history. The
current label is a projection over:

- the entry identity;
- non-expired observations;
- live grant, lease, bundle, and contract state;
- the current plan version and step frontier.

A scalar `method` would overwrite history. Instead, every discovery method
records an observation. If static analysis proposes a shape, collision confirms
it, and the operator approves it, all three facts remain available. If a
proposed lookahead label expires before any contract exists, the observation or
entry expiry is enough to shed it.

Lifecycle status is monotonic. An entry may move from partial knowledge toward a
proposed or approved label, and then to a terminal status: consumed, expired, or
invalidated. Direct partial-to-approved transitions are permitted for imported or
already-live authority evidence that never needed a separate proposal card. A
direct partial-to-terminal transition is permitted when the system learns that a
shape was invalid, expired, or already consumed before it became useful. What is
not permitted is implicit downgrade, implicit reopening, or silent expiry
extension. Extending an expiry is a material lifecycle decision and must be
explicit at the writer boundary.

### Truth Class

The identification ledger is canonical for authority discovery state:

- What has this run discovered?
- Which step and session was the discovery bound to?
- Which properties are known, and which observations produced them?
- Was the identified authority consumed, expired, or invalidated?

Operator cards are projections of this ledger plus grant/lease state. They are
not the ledger.

## Menu Compilation

The persona-facing menu is a projection:

```text
identification ledger
  join live grant/lease/bundle state
  join current plan frontier
  -> executable authority menu
```

The menu is not a document that drifts. It is a query. Recompilation is reading
the ledger and current grant state again.

The persona agent should see only:

- tokens whose authority story is currently satisfiable;
- tokens one approval away from satisfiable;
- a standing off-menu route into the capability/recovery request path.

This is the replacement target for natural-language, phrase-heavy child or
durable-agent routing heuristics. The model can interpret messy user intent, but
authority affordances should come from the compiled menu.

## Role Separation

The menu exists to keep the user-facing persona out of kitchen grammar.

The user-facing persona should behave like a waiter with a live menu:

- select the closest menu token for the current user intent;
- attach user notes and preferences;
- surface ambiguity when no menu token is close enough;
- avoid constructing grants, leases, retry contracts, JSON payloads, or recovery
  handoffs directly.

The system compiler should behave like the kitchen:

- expand the token and notes into workflow candidates;
- derive workflow terms such as tool, action, principal, resource, lease class,
  retry operation, and constraints;
- check current ingredients: live grants, leases, bundles, policies, task state,
  and evidence;
- produce resolution sets when more than one safe path exists;
- compile the selected path into typed contracts and next actions.

The presentation layer is a fourth role, but not an AI role. It is deterministic
projection:

```text
typed record
  -> faithful Telegram card / button / status text
```

It should render contracts, ledger entries, and resolution sets. It should not
invent authority, infer missing schema terms from prose, or make approval
binding decisions outside the typed review state machine.

This separation fixes the failure mode where a persona misses a brittle input
shape and the system agent merely rejects it. The persona should choose
`carbonara` and add "extra black pepper"; the compiler should know how carbonara
is cooked, what ingredients are missing, and which safe alternatives can be
offered. In Aphelion terms: the persona chooses a menu token and notes; the
system owns authority compilation.

## Identification Methods

### Collision

Collision is identify-by-use. A plan attempts a concrete tool operation. If
authority is missing, the tool boundary records the blocker and compiled
contract. No effect occurs.

This is already the strongest current substrate:

- `CompileContinuationRecoveryContract`
- `RecordContinuationRecoveryContractNextAction`
- missing-grant review requests
- missing-lease request approval handoffs
- child task blocker classes

The ledger should be written in the same transaction as the recovery contract
and next action whenever possible.

### Static

Static identification is price identification. The runtime examines declared
plan steps, tool/action pairs, manifests, and current grant state without
executing the step.

Examples:

- a plan step declares `durable_agent wake_once` for a named child;
- an external tool manifest declares its timeout and required lifecycle;
- a file operation declares a path under a grant root;
- a capability request already names target, principal, and allowed actions.

Static analysis may create proposed ledger entries, but it must not materialize
execution authority by itself.

### Operator

Operator identification is the scroll of identify. A review card asks the human
to approve, reject, narrow, or choose among a resolution set.

The card must render a typed record. It may not invent authority from prose.
Operator actions that mutate the discovery frontier, such as `Next grant` taps
and approval clicks, should be recorded as observations with actor provenance.
The ledger must be able to say not only "this shape was learned," but which
operator action advanced the frontier.

### Lookahead

Lookahead is speculative identification. It asks: from the current plan
frontier, what is the next authority collision we can prove without executing
anything?

Lookahead should be exposed as a small operator action, likely a `Next grant`
button on a successful approval card. Pressing it does not approve execution.
In the full Ralph-loop implementation it should spend one bounded lookahead
allowance and produce the next proposed card.

Hard constraints:

1. One press spends one durable lookahead allowance.
2. Lookahead authority is not execution authority.
3. A lookahead press may compile a clustered frontier into one authority bundle
   card, but not competing pending continuations.
4. Speculative approvals expire or invalidate when the bound step or plan
   version stops matching.

This gives operators a burst mode for long plans without reintroducing broad
pre-granting. A user can walk the authority frontier before going offline, but
the tail remains narrow, step-addressed, and perishable.

Lookahead is metered as an outstanding-frontier budget, not a time cooldown. The
default budget is five unresolved lookahead allowances per admin chat across
active plans. Each allowance is a durable row bound to the admin, source/target
session, review event, surfaced next action, and identification-ledger entry.
When the budget is full, `Next grant` must not simulate, publish, approve,
execute, or write ledger labels; it should only tell the operator to resolve,
reject, or let one existing frontier expire. Allowances are released when their
ledger entry becomes approved or terminal, and otherwise expire. This bound is a
tail-size invariant: it limits the amount of speculative authority work that can
trail the operator, while still allowing fast burst identification when slots
are available.

The meter must be inspectable without SQLite archaeology. The operator-facing
frontier view is a projection of the allowance ledger: five slots, each showing
empty, reserved, open, or expired state, the bound review event, next action or
ledger entry, and its TTL. It is not an approval surface and must say so
plainly: viewing the frontier approves and executes nothing.

The meter also beats. Slot reservations, surfaced approvals, releases, expiry,
and budget-pressure stops emit `authority.frontier.delta` execution events.
The frontier projection should show recent deltas and inter-arrival timing for
repeated `shape_hash` collisions, because a recurring shape across shifting
`step_ref`s is itself evidence: the plan is finding the same authority rhythm in
different places.

Silence is an actor. If an operator lets a lookahead card expire, the matching
ledger entry receives an `operator_absence` observation with action
`expired_unreviewed`; this is distinct from rejection. Non-response spends
operator-interruption budget and must be measurable rather than disappearing as
mere timeout cleanup.

Executable menu state requires a live witness. A loadout or ledger label is not
executable because it was mentioned; it is executable only when the runtime has
joined it against current grant/lease/contract state and produced a
`LiveSlotWitness`. The witness is a local proof of point-of-use validation, not
an authority object.

The first executable implementation makes this a real control-plane loop over
the current recovery frontier and a bounded deterministic simulation of the
active `OperationPhasePlan`. The button authenticates the private admin
callback, reserves one lookahead allowance before any simulation can publish a
candidate, then resolves an exact frontier when the review event names one.
Otherwise it scans the review event's source session for unresolved
contract-backed `request_approval` next actions. If none exists, it simulates
forward through pending or in-progress phases with unmet
`RequiredCapabilityGrants`, compiles the discovered phase frontier into one
authority-bundle contract, records a `request_approval` next action,
materializes the normal approval card for that exact stored contract, binds the
allowance to the surfaced next action and ledger entry, and records ledger
observations only after materialization succeeds. If no card is surfaced, the
reserved allowance is released. It does not approve authority and it does not
execute the protected operation.

The bundle is the important shape. The runtime should not open several
simultaneous pending continuations from one lookahead press; that would create
approval-state competition. A deep frontier should batch into one narrow,
step-bound, expiring authority bundle whose components name the simulated
phases and whose required grants carry the exact action/resource constraints.

This simulator is intentionally still narrower than a complete Ralph-loop
interpreter. It covers already-discovered recovery frontiers and declared
operation-phase capability requirements. Future simulation can cover richer
step grammars, manifests, and resolution sets, but it must produce the same kind
of stored contract and next action before projection.

This first slice is intentionally single-principal. A `Next grant` spend belongs
to the admin principal that received the private review event. Plural authority
is a different contract: allowances would need per-principal reservation,
quorum/unanimity semantics, and revocation when one principal leaves. It should
not be smuggled into this schema by treating one `ActorPrincipal` string as a
committee.

## Resolution Sets

A blocker often has more than one safe repair:

- reuse an existing grant;
- request a narrower grant;
- request a broader but bounded bundle;
- ask for operator rewrite;
- stop and wait for external state.

The recovery surface should eventually represent those choices as a resolution
set: multiple precompiled candidates, one selected by approval. Selection is
approval. The model can explain the choices, but the runtime must validate the
chosen candidate against its compiled contract.

This avoids hardcoding one repair path per blocker while keeping the review
surface deterministic.

## Tail Shedding

The design depends on aggressive shedding:

- one-time continuation leases are consumed;
- child task attempts are fenced and expire;
- authority bundles carry material expiry;
- stale cards cannot bind to newer next actions;
- wrong-session or wrong-plan pointers terminalize;
- speculative entries invalidate on plan revision;
- approved-but-never-consumed entries expire.

A long plan is allowed to accumulate authority only while that authority remains
live for a current step. Authority that outlives its reason is stale tail.

## Current Strengths

Post-PR 282, Aphelion already has much of the substrate:

- typed recovery contracts for safe collisions;
- transactionally published contract plus next action;
- authority bundles with expiry;
- one-time leases, generations, and fenced child-task attempts;
- deterministic review-card projection;
- child wake progress and typed blockers;
- active-grant and point-of-use validation.

The identification game should build on those pieces instead of inventing a
parallel approval model.

## Current Gaps

The first executable slice adds the identification ledger, collision
publication, operator-provenance observations, a durable lookahead allowance
ledger, a pure menu projection, a store-backed live-authority menu join,
redacted authority archetype helpers, stable resolution-candidate metadata, an
initial menu-token route for natural durable-child requests, a typed child result
contract at the durable wake boundary, allow-list parent-to-child context
construction, and a non-executing `Next grant` control that surfaces the next
real contract-backed approval frontier or simulates a clustered operation-phase
capability frontier into one authority-bundle approval card. The architecture
still lacks:

- first-class plan objects and plan-version reshuffle triggers;
- lookahead simulation for plan steps beyond `OperationPhasePlan`
  capability requirements;
- fully rendered multi-option resolution-set cards where selecting candidate A
  can never materialize candidate B;
- child adapters that emit typed result contracts natively, rather than relying
  on the `APHELION_CHILD_RESULT` text transport compatibility path;
- full persona routing through compiled menu tokens across all governed task
  families, not only the initial durable-child natural-request lane.

These are not all one PR. They are the frontier this draft names.

## File-Level Direction

Likely future surfaces:

- `session/types_plan_ledger.go`
- `session/store_plan_ledger.go`
- `runtime/plan_ledger.go`
- `runtime/menu_compiler.go`
- `runtime/lookahead.go`
- `runtime/plan_static_identify.go`

Initial implementation slice:

- `session/types_identification_ledger.go`: ledger entry and observation
  signatures.
- `session/store_identification_ledger.go`: append-only observation history and
  query projection.
- `session/identification_ledger_schema.go`: v88 ledger migration and v89 actor
  provenance plus lookahead allowance migration.
- `session/types_lookahead_allowance.go` and
  `session/store_lookahead_allowances.go`: durable unresolved-frontier budget
  for `Next grant`.
- `runtime/authority_discovery_menu.go`: deterministic menu and local metrics
  projection, stable resolution-candidate metadata, and the store-backed
  live-authority join.
- `core/review_event_callback.go` and
  `internal/telegramdecision/telegram_decisions_review.go`: `Next grant`
  callback token and confirmation-card projection.
- `runtime/review_event_child_wake_retry.go`: private-admin `Next grant`
  handling over the current contract-backed recovery frontier.
- `runtime/authority_discovery_lookahead.go`: first non-executing lookahead
  frontier selector over unresolved recovery handoffs plus clustered
  operation-phase required capability grants.
- `runtime/durable_agent_natural_request.go`: initial durable-child
  menu-token routing, with mention resolution kept as routing context and typed
  recovery envelopes excluded from natural handling.
- `runtime/durable_wake.go`: typed child result contract preference and
  allow-list child context value boundary.
- `session/authority_archetype.go`: redacted authority archetype projection for
  deriving reusable examples from stored contracts without exposing live values.

Likely extensions:

- `session/types_continuation_recovery.go`: add plan identity terms to future
  contract versions or compatibility canonicalization.
- `runtime/continuation_materialize.go`: continue to centralize
  `request_approval` handoff materialization; later materialize resolution sets,
  not only single handoffs.
- `runtime/typed_continuation_approval.go`: consume selected candidates.
- `internal/telegramdecision/telegram_decisions_review.go`: render
  resolution-set cards.
- `tool/request_approval_*`: render bundle-backed multi-option approvals.

## Test Model

The interesting tests are optimization tests over authority discovery, not only
happy-path approval tests.

Primary invariant:

```text
authority violations = 0
```

No plan step may execute outside a live, admitted, correctly bound grant or
lease. This is categorical.

Secondary metrics:

- `interruptions`: operator touches required to complete a plan;
- `coverage`: distinct authority classes identified for the current plan
  frontier;
- `over_grant_mass`: approved authority never consumed, weighted by scope and
  lifetime;
- `wasted_collisions`: blockers that teach the ledger nothing new;
- `tail_area`: integral of live-authority count over time;
- `shape_errors`: compiler rejections caused by producer-side guessing.

Harness shape:

- fixture plans with 20 to 100 steps;
- seeded hidden authority ontologies;
- simulated operator policies:
  - never look ahead;
  - always press `Next grant`;
  - fixed loadout of one or two plan-start gifts;
  - greedy burst of `k` lookahead grants, then sleep;
  - oracle lower bound;
- restart chaos at every progress phase;
- metamorphic reshuffle tests across sessions and plan versions.

Regression posture:

- A change may trade interruptions against bounded over-grant mass only by
  explicit design choice.
- It must never increase authority violations.
- It should increase coverage before increasing raw approval count.
- It must never let approved authority survive plan revision, run end, expiry,
  or step mismatch.

## Stochastic Authority Traces

Long-running language agents are stochastic processes. The same plan frontier,
prompt, and model slot can produce different sampled paths. Aphelion should not
try to make those paths deterministic by trusting prose. It should make them
comparable by recording typed authority traces.

The trace alphabet is made of records the runtime already understands or should
make first-class:

- blocker class;
- continuation recovery contract;
- capability grant or lease request;
- review-card action;
- child task packet, attempt, result, and blocker;
- authority admission and consumption;
- stale-tail invalidation;
- terminal outcome.

The identification ledger is therefore more than memory for one run. It is the
empirical substrate for comparing agent behavior under the same authority
policy. Two models, prompts, or child implementations are equivalent for an
Aphelion workflow only when they preserve hard invariants and induce acceptably
close distributions over typed authority traces.

Hard invariants remain categorical: unauthorized effects must never execute.
Optimization then happens over trace distributions: interruptions, coverage,
over-grant mass, wasted collisions, tail area, and shape errors.

## Design Rule

One-time authority should be discovered, labeled, and consumed inside the run.
Durable authority types belong in code only when they provide enforcement
semantics or useful aggregate labels across repeated workflows.

Everything else should flow through the identification game:

```text
unknown shape
  -> safe collision or static/lookahead proposal
  -> typed contract
  -> ledger label
  -> operator projection
  -> bounded approval
  -> consumption or shedding
```

## References

- Benjamin Soul, `Serpentes`: <https://benjamin-soul.itch.io/serpentes>.
- Ralph Loops: <https://ralphloops.io/>.
- Ralph Workflow: <https://ralphworkflow.com/>.
- Robin Milner, *Communication and Concurrency*; C. A. R. Hoare,
  *Communicating Sequential Processes*; Kohei Honda et al. on session types.
  These are background references for typed interaction rather than required
  implementation dependencies.
