# Predictive Interception

_Status: draft architecture direction._

Predictive interception is goal-backward execution for Aphelion. It is the
controller that should eventually consume a selected attention track and pursue a
goal by steering toward where the goal will be satisfied, not merely toward the
next prewritten step.

The motivating image is a dragonfly intercepting prey: it does not chase the
current position; it continuously re-projects the intercept point. In Aphelion,
the mechanism is not literal proportional navigation. It is event-triggered
receding-horizon control over typed evidence.

The smaller invariant is:

> Hold the goal spec to the same standard as everything below the gate.

Today Aphelion is strict below the action boundary: no effect counts as complete
without evidence and no boundary is crossed without authority. The goal that
defines "done" is weaker: it is often operator text, asserted once, then carried
as context. Predictive interception makes the goal a versioned acceptance
contract, evaluates it against attested evidence, and re-aims the disposable
plan when evidence says the current path is no longer the cheapest valid path.

## Relationship To Attention Radar

[`attention-radar.md`](attention-radar.md) tracks live recommendable surfaces.
Predictive interception is what happens after one of those tracks becomes an
active goal-bearing pursuit.

```text
attention radar
  -> selected track
  -> goal acceptance contract
  -> evidence projection
  -> re-aim / ask / stop / propose boundary action
```

Radar decides what deserves attention. Interception decides how to close the
goal gap once the operator chooses a track or starts a pursuit.

## Non-Goals

- No new orchestration platform or dashboard.
- No autonomy that bypasses approval gates.
- No model-authored reward function that silently defines success.
- No replacement for operator final acceptance.
- No recursive sub-operation tree in the first slice.

## Control Model

The goal is the durable invariant. The plan is disposable.

The loop reads evidence, projects goal satisfaction, decides whether the current
plan is still valid, then proposes the next bounded action through existing
authority gates.

```text
goal acceptance contract
        |
        v
evidence snapshot -> goal projection -> trend / attribution
        |                                |
        |                                v
        +------------------------- re-aim decision
                                         |
                                         v
                           bounded plan proposal -> gate -> work
                                         |
                                         v
                                  new evidence
```

The controller may change what is proposed and when. It must not change whether
authority is required.

## Typed Acceptance Contract

The keystone is a goal acceptance contract. The evaluator should be mostly
deterministic over this contract and an evidence snapshot.

Illustrative shape:

```go
type GoalContract struct {
    GoalID      string
    GoalSpecVer int
    Objective   string
    Criteria    []AcceptanceCriterion
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type AcceptanceCriterion struct {
    ID          string
    GoalSpecVer int
    WorldState  string // predicate about the world we want true
    EvidenceQry string // attested evidence that can prove WorldState
    Freshness   string // staleness / invalidation policy

    Progress    *ProgressStateMachine // only when partial progress is real
    Method      string // deterministic | advisory_judge
    Criticality string // required | soft
    DependsOn   []string
}

type CriterionState struct {
    ID       string
    State    string // satisfied | progressing | open | unknown |
                    // blocked | invalidated
    Residual *float64 // present only when Progress exists
    Evidence []string
}

type GoalProjection struct {
    GoalID       string
    GoalSpecVer  int
    EvaluatorVer int
    SnapshotID   string
    Criteria     []CriterionState
    Satisfied    bool
}
```

The separation between `WorldState` and `EvidenceQry` matters. The planner must
not learn to optimize demonstrability instead of reality. Evidence proves a
named world-state predicate; it is not a generic token that can satisfy any
nearby goal.

Projections are evidence objects, but they are not eligible to ground future
acceptance. A projection saying "criterion satisfied" is evidence that the
projection was produced, not evidence that the criterion is true.

## Partial Order, Not Universal Distance

For many goals, a scalar distance is fake precision. "Tag exists and was pushed"
is binary. "Release notes cover N of M merged PRs" can have real partial
progress. The controller should use a partial order:

- improved;
- regressed;
- incomparable;
- unknown;
- blocked.

A scalar can be derived for an operator card only when useful, but control
decisions should not depend on a universal `0.43 done` number.

Continuity is earned per criterion through a real progress state machine.

## Re-Aim Triggers

A re-aim is event-triggered. It should happen when evidence says one of these is
true:

- current plan became infeasible;
- evidence regressed;
- evidence expired or was invalidated;
- a cheaper valid plan is available;
- goal reference likely changed;
- attribution is indeterminate and needs the operator.

Shortcuts and repairs are the same kind of operation: update the plan while
preserving the goal contract.

## Attribution

When the evidence state worsens, the system must classify why:

| Cause | Route |
| --- | --- |
| Plan regressed | Re-aim the plan |
| Evidence expired | Re-gather evidence |
| Delayed evidence arrived | Re-project, usually no re-aim |
| Goal/evaluator version skew | Re-baseline |
| Third actor changed the subject | Alert and re-assess |
| Criterion appears wrong | Propose amendment |
| System cannot tell | Ask the operator |

The hard case is self-reference: the criterion defines what evidence matters,
and the system suspects the criterion itself is wrong. That cannot be fully
closed from inside the same system. The correct behavior is not cleverer
guessing. It is abstention:

> I cannot tell whether the work regressed or the target is wrong.

That is an operator question.

## Reference Gate

The plan is disposable; the goal reference is governed.

Changing an acceptance criterion is a boundary operation against the definition
of done. It must be versioned and reviewable.

Rules:

- Every amendment bumps `GoalSpecVer`.
- Old projections cite the goal version and evaluator version used.
- Tightening a criterion may batch with an already owed approval.
- Weakening or dropping a criterion always interrupts and is labeled as making
  done easier.
- Reference amendments are never auto-approved by a pursuit lease.

This prevents the controller from moving the goalposts to declare success.

## Pursuit State

Illustrative state machine:

```text
pursuing
  -> awaiting_approval
  -> reaiming
  -> criteria_satisfied
  -> operator_accepted
  -> stalled
  -> halted_cost
```

Important distinction:

- `criteria_satisfied`: evaluator found enough evidence.
- `controller_converged`: no further useful work is available.
- `operator_accepted`: human closed the mission.

Evaluator satisfaction is not consent.

## Operator Surface

The operator should see criterion state, not a fake progress bar:

```text
Pursuing: ship v0.3

Criteria:
- CI green: satisfied, evidence ci_run:4812
- Release notes cover merged PRs: progressing, 12/14
- Tag pushed: open

Next boundary:
push refs/tags/v0.3 only

[Approve] [Pause] [Why]
```

When re-aiming:

```text
Re-aimed: cheaper path found.

The release job already created the tag, so I dropped the "create tag" phase.
Remaining boundary: push existing tag.
```

When indeterminate:

```text
I cannot tell whether the notes criterion is wrong or the work regressed.

[Keep working] [Amend criterion] [Show evidence]
```

The surface remains Telegram and CLI. Predictive interception adds a clearer
projection, not a dashboard.

## Validation

Predictive interception needs long-horizon evals. The key question is not "did a
turn sound good?" It is "did the pursuit reach the goal under bounded authority
and cost?"

Metrics:

- success rate over rollouts;
- median tokens-to-goal;
- interruptions per pursuit;
- useful re-aim rate;
- stale-goal dwell time;
- abstention calibration for ambiguous regressions;
- false-success rate.

The in-loop evaluator and the eval oracle should share contracts and fixtures,
but not the same verdict implementation. Otherwise the benchmark can inherit the
same bug as the controller.

Boundary-attack tests:

- A projection object cannot satisfy a criterion.
- A model assertion cannot satisfy a deterministic criterion.
- A stale goal version cannot accept under a new version.
- A weakened criterion cannot be silently adopted.
- Advisory criteria cannot auto-close a mission.
- Authority-bound steps still require the ordinary gate.

## File-Level Direction

Leverage:

- `session/mission_types.go`: mission objective, success criteria, evidence
  checklist, budget.
- `session/types_operation.go`: operation and phase plan model.
- `session/types_evidence.go`: evidence objects and links.
- `runtime/goal_continuation.go`: existing single-step re-projection seed.
- `runtime/reentry_recommendation.go`: current advisory next-step surface.
- `runtime/continuation_loop.go`: bounded continuation budget.
- `runtime/continuation_supersession.go`: stale card/approval retirement.
- `runtime/eval.go`: trajectory and live-eval substrate.

Likely additions:

- `session/types_goal_contract.go`
- `session/store_goal_contracts.go`
- `runtime/goal_projection.go`
- `runtime/goal_reaim.go`
- `runtime/goal_pursuit_controller.go`
- `runtime/eval_goal_pursuit.go`

## Rollout

1. Goal acceptance contract scaffold and versioning.
2. Deterministic evaluator over contracts and evidence snapshots.
3. Independent eval oracle for goal satisfaction.
4. Operator projection card for criterion states.
5. Trend analyzer and re-aim trigger.
6. Reference gate for criteria amendments.
7. Bounded goal-seeking controller.

The controller should wait until the evaluator and oracle are credible. Without
that spine, predictive interception becomes a model-authored reward loop.

## Design Rule

Predictive interception may change what Aphelion proposes and when it proposes
it. It must never change whether authority is required, whether evidence is
needed, or whether the operator owns final acceptance.
