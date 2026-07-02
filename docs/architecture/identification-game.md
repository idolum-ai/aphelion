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

## Ralph Loop As Run

A Ralph loop is a plan/execute/verify machine that advances only after durable
evidence proves the previous step completed. In this model it is the run.

- The dungeon is the plan: a partially ordered set of steps.
- The shuffle seed is the session, plus plan identity and plan version.
- The evidence gate is the durable state that proves a step completed.
- The tail is every live grant, lease, bundle, and speculative approval the run
  has accumulated.

The run's authority question is:

> What authority shapes has this plan identified, by which method, and are they
> still live for the same session, plan version, and step?

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

    ShapeHash string
    Method    string // collision | static | lookahead | operator
    Label     string // contract/bundle/grant ref, or unknown
    Status    string // unidentified | proposed | approved | consumed | expired | invalidated

    CreatedAt time.Time
    UpdatedAt time.Time
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

### Truth Class

The identification ledger is canonical for authority discovery state:

- What has this run discovered?
- Which step and session was the discovery bound to?
- Did the discovery come from a collision, static analysis, lookahead, or an
  operator action?
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

### Lookahead

Lookahead is metered speculative identification. It asks: from the current plan
frontier, what is the next authority collision we can prove without executing
anything?

Lookahead should be exposed as a small operator action, likely a `Next grant`
button on a successful approval card. Pressing it does not approve execution.
It spends one bounded lookahead allowance and produces the next proposed card.

Hard constraints:

1. One press identifies at most one next collision.
2. Lookahead authority is not execution authority.
3. Speculative approvals are bound to session, plan version, and step.
4. Speculative approvals expire or invalidate when the bound step or plan
   version stops matching.

This gives operators a burst mode for long plans without reintroducing broad
pre-granting. A user can walk the authority frontier before going offline, but
the tail remains narrow, step-addressed, and perishable.

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

The architecture still lacks:

- plan-level memory of discovered authority shapes;
- a compiled menu over that memory;
- lookahead as a typed non-executing action;
- resolution sets for repair choices;
- typed child result objects at the parent boundary;
- allow-list child context construction;
- a replacement for phrase-heavy durable-agent natural request routing.

These are not all one PR. They are the frontier this draft names.

## File-Level Direction

Likely new surfaces:

- `session/types_plan_ledger.go`
- `session/store_plan_ledger.go`
- `runtime/plan_ledger.go`
- `runtime/menu_compiler.go`
- `runtime/lookahead.go`
- `runtime/plan_static_identify.go`

Likely extensions:

- `session/types_continuation_recovery.go`: add plan identity terms to future
  contract versions or compatibility canonicalization.
- `runtime/continuation_materialize.go`: materialize resolution sets, not only
  single handoffs.
- `runtime/typed_continuation_approval.go`: consume selected candidates.
- `internal/telegramdecision/telegram_decisions_review.go`: render `Next grant`
  and resolution-set cards.
- `core/review_event_callback.go`: add a lookahead callback action.
- `tool/request_approval_*`: render bundle-backed multi-option approvals.

Likely replacements:

- `runtime/durable_agent_natural_request.go`: move from phrase heuristics to
  menu-token resolution.
- `runtime/durable_wake.go`: retire status derived from summary text; commit
  typed child result status first, projection second.
- `runtime/durable_wake.go`: move parent-to-child turn context from deny-list
  stripping to allow-list construction.

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
  - greedy burst of `k` lookahead grants, then sleep;
  - oracle lower bound;
- restart chaos at every progress phase;
- metamorphic reshuffle tests across sessions and plan versions.

Regression posture:

- A change may trade interruptions against bounded over-grant mass only by
  explicit design choice.
- It must never increase authority violations.
- It must never let approved authority survive plan revision, run end, expiry,
  or step mismatch.

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

