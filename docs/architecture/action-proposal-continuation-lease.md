# ActionProposal and ContinuationLease

`ActionProposal` and `ContinuationLease` are the v1 contract for turning a
model-authored “I can continue / execute this bounded plan” into explicit human
approval without widening authority by vibe.

## Purpose

- `ActionProposal` names the proposed bounded action contract.
- `ContinuationLease` is the consumable authorization derived from an approved
  proposal.
- Approval callbacks carry proposal/lease identity, not freeform authority.
- Compound-plan approvals are sealed as per-phase approval tokens. They are not
  blank checks for the whole plan.
- The lease is consumed, revoked, or expired by runtime state; it does not grant
  new tools, accounts, devices, purchases, or public effects by itself.

## Current Storage

For v1, both records are embedded in
`session.ContinuationState` / `sessions.continuation_state_json`.

This keeps the current Telegram `Start` / `Stop` flow intact while making the
implicit continuation prompt contract typed and auditable.

## ActionProposal Fields

The typed record includes:

- `id`
- `operation_id`
- `mission_id`
- `operator_title`
- `plan_title`
- `summary`
- `why_now`
- `bounded_effect`
- `risk_class`
- `allowed_actions`
- `forbidden_actions`
- `validation_plan`
- `expires_at`
- `plan_hash`
- `status`

Runtime builds a proposal when continuation consensus is eligible. The proposal
is pending until the user presses `Start`, then approved if still fresh.

## ContinuationLease Fields

The typed record includes:

- `id`
- `proposal_id`
- `mission_id`
- `status`
- `max_turns`
- `remaining_turns`
- `approved_by`
- `lease_class`
- `constraints`
- `allowed_actions`
- `forbidden_actions`
- `validation_plan`
- `required_capability_grants`
- `expires_at`
- `plan_hash`
- lifecycle timestamps

The current v1 lease remains bounded by a short TTL and explicit turn budget.
Ordinary continuations may be one-turn leases; materialized operation phase
bundles and explicit multi-turn leases may carry more than one turn. On trigger,
stale leases expire closed. On execution, each turn is consumed before the
machine-authored continuation event is processed.

## Lifecycle

1. A turn produces both persona and governor continuation intent.
2. If both are eligible and governor-ratified, runtime creates:
   - pending `ActionProposal`
   - pending `ContinuationLease`
   - pending continuation state
3. Telegram renders a bounded approval card. Ordinary continuations use
   `Start` / `Stop`; approval bundles use `Approve all` / `Approve current`
   plus status/change/stop controls.
4. `Start`, `Approve all`, or `Approve current` approves only the sealed
   proposal/token envelope that is still fresh.
5. Runtime triggers the approved continuation path. If the approved lease
   remains active, has remaining turns, still matches the sealed bundle or
   proposal, and mission state does not impose a stop, runtime may continue
   consuming approved turns automatically.
6. Consuming each turn decrements the lease; zero remaining turns marks it
   consumed and returns continuation state to idle. The loop never renews,
   widens, or reinterprets the lease.
7. `Stop` revokes the continuation and lease.
8. Expired proposals/leases fail closed and emit `continuation.blocked`.

## Default-on continuation loop

After approval, runtime reuses the existing approved-continuation trigger and
execution path for every automatic follow-up turn. There is no parallel
continuation executor.

Automatic in-lease continuation stops when any boundary is reached:

- continuation state is no longer approved;
- `RemainingTurns` or lease `remaining_turns` reaches zero;
- the lease expires, is revoked, or is consumed;
- an approval-only plan lease has no active executable bundle;
- a bundle fingerprint no longer matches the current operation phase plan;
- mission state is completed, blocked, archived, expired, or dormant;
- the next proposed work would require wider authority, new actions, or new
  required capability grants.

Each automatic follow-up turn emits a compact Telegram progress line before it
starts. Boundary stops are recorded in TES as
`continuation.boundary_reached`.

## Compound plan approval bundles

A durable phase plan may materialize as an `approval_bundle` when multiple
phases are specific enough to ask about together. The bundle exists to reduce
approval friction without turning the plan into ambient authority.

Each bundled phase is stored as a sealed token:

- the bundle records the operation id, phase-plan id, and plan fingerprint;
- each phase token records its phase id, authority envelope, validation plan,
  required grants, and phase fingerprint;
- approval and trigger paths compare the stored fingerprints against the current
  operation phase plan before execution.

The user-facing buttons mean:

- `Approve all`: approve the currently sealed phase tokens in the bundle. Each
  phase is still consumed only when it becomes current; approval does not grant
  unrelated work or later mutated phase text.
- `Approve current`: approve only the current sealed phase token. Unselected
  tokens are marked deferred, left pending in the operation phase plan, and must
  be re-prompted before use.
- `Details`: show the bounded effect, allow/stop lists, and the bundle warning
  that approval is per-phase sealed authority.
- `Change`, `Pause`, and `Stop`: avoid approving the prompt.

If the operation, phase plan, bounded effect, authority class, allowed/stopped
actions, validation plan, or required grants drift after the card is shown, the
bundle becomes stale. A stale bundle is rejected before approval or trigger and
the operator must use the newest prompt. This is intentional: old buttons cannot
approve a changed plan.

When an approved bundle is active, execution prompt text is phase-focused: the
current sealed bundle phase supplies the next step, bounded effect, authority
class, allowed actions, and forbidden actions. The broader bundle title is
presentation, not executable scope.

Runtime records `continuation.bundle.narrowed` when a phase-plan approval is
observed as too narrow across related phases. Compile/repair events record
self-block repair evidence when a proposed continuation contract is repaired,
exhausted, or too ambiguous to repair.

## Active lease reuse

When a fresh operation phase or proposal would otherwise ask for approval while
an approved lease is still active, runtime may consume it under that active
lease instead of re-prompting. Reuse is conservative:

- the prior continuation must still be approved and active;
- the proposed lease class must match the active lease class;
- every proposed allowed action must pass `CheckContinuationLeaseAction` against
  the active lease;
- required capability grants must already be covered by the active lease;
- local-workspace leases cannot adopt proposed external effects.

Remote repository publication is its own lease class. A local-workspace or local
commit lease cannot absorb `git_push` work by class reuse; the push must be
present as explicit typed `git_push` authority inside a repo-publication lease
or a stronger explicitly-approved release/deploy lease.

This is approval-friction reduction only. It does not create authority, grant
capabilities, or auto-renew the lease. Runtime records successful reuse as
`continuation.class_scoped_consumption`.

## Non-Authority Rule

A `ContinuationLease` authorizes only continuation under existing authority and
bounded effect. Any expanded capability still goes through the capability
request/review/grant lane. A button-backed phase approval may approve the named
required capability grants in the same bounded path, but approval-window
auto-approval must not mint capability grants, and grant expiry defaults to the
continuation lease expiry when the grant spec does not provide one.

## Code Anchors

- [`session/types.go`](../../session/types.go)
- [`session/store.go`](../../session/store.go)
- [`runtime/continuation.go`](../../runtime/continuation.go)
- [`runtime/continuation_work.go`](../../runtime/continuation_work.go)
- [`runtime/continuation_loop.go`](../../runtime/continuation_loop.go)
- [`runtime/continuation_class_scope.go`](../../runtime/continuation_class_scope.go)
- [`internal/telegramcommands/commands_continuation.go`](../../internal/telegramcommands/commands_continuation.go)


## Generic ActionProposal UI v1

The first generic UI surface is mission-review backed:

- `/mission propose <mission_id>` renders a Mission Proposal card with Telegram
  `Reject`, `Change`, and `Approve` buttons.
- Callback data is keyed by the proposal id (`action_proposal:<proposal_id>:<action>`).
- For mission-backed proposals the proposal id currently derives from the
  mission id (`aprop-<mission_id>`).
- `Approve` marks a candidate/dormant mission active for review/planning only.
- `Change` leaves the mission candidate and records `waiting_for=proposal_edit`.
- `Reject` leaves the mission review-only and records `waiting_for=proposal_denied`.

This v1 UI is intentionally not a tool execution grant and does not create a
self-continuation lease. It makes the approval control surface real while
keeping actual execution authority in later, bounded ActionProposal or
ContinuationLease requests.


## Mission Review Proposal Card

Mission proposal cards are pre-ActionProposal intake gates. They let the
system suggest a candidate mission with inline buttons before the mission exists
in the ledger.

Buttons:

- `Reject`: reject the idea; no mission is created.
- `Add mission`: create a candidate mission with default review-only
  authority.
- `Park`: leave the idea untracked for now; no mission is created.
- `Change`: request a revised proposal; no mission is created.

This card deliberately does not authorize execution. It only decides whether an
idea becomes a candidate mission that may later receive an ActionProposal /
ContinuationLease.

## Continuation controls v2

Continuation prompts now use short explicit lease-control buttons instead of a
single ambiguous `Continue` button. Newly rendered Telegram labels are:

- `Start`: approve the pending ContinuationLease exactly as written and trigger
  the bounded continuation. If the lease carries multiple executable turns,
  runtime may continue automatically inside the approved lease until a boundary
  is reached.
- `Details`: render the current continuation edge without mutating state.
- `Change`: revoke/park the pending continuation prompt and ask for a revised
  lease. It does not trigger continuation.
- `Pause`: revoke/park the pending or approved continuation prompt.
- `Stop`: revoke continuation approval using the existing stop/revoke path.
- `Run`: for post-boundary recovery, resume only if a lease is already approved
  and has remaining turns; otherwise it reports that approval is still needed.
- `Refresh`: show the current edge and ask for the next explicit lease after an
  expired prompt; it does not approve or trigger work.

Callback note: approval buttons use the current `approve_lease` action. Removed
callback actions such as `continue` and bare `approve` are rejected as stale.

Authority rule: status and edit buttons must not grant execution authority.
Only `Start` can activate the lease, and activation still uses the persisted
proposal/lease identity rather than freeform text.

Deploy/restart remains a standalone hard gate. A deploy lease may cover commit,
build, install, restart, and post-restart verification only when those actions
are named in the proposal body and approved as a deploy/restart phase; ordinary
plan leases stop before deploy/restart authority.

## Operation-proposal lease materialization

Assistant-authored bounded operation proposals should not remain plain text when
button transport is available. After the visible reply is delivered, a pending
`OperationProposal` with a stable id is materialized into the existing
`ContinuationState` shape:

- `ActionProposal.OperationID` points back to the operation proposal id.
- `ContinuationLease` carries a one-turn pending lease with the proposal hash.
- Telegram receives the compact continuation-control buttons (`Start`,
  `Details`, `Change`, `Pause`, `Stop`, with `Run`/`Refresh` on edge states).
- Approving the continuation also marks the matching operation proposal
  approved; revoking/parking the pending lease marks it denied so the same ask
  is not re-offered endlessly.

This is intentionally narrower than parsing arbitrary assistant prose. The first
materialization path requires a structured pending `OperationProposal` so the
button carries an auditable proposal id and bounded effect. Plain text remains a
fallback, not the primary approval surface.

Telegram `callback_data` is capped at 64 bytes. Continuation buttons therefore
use a compact deterministic alias when a human-readable proposal id would make
`continuation:<id>:<action>` too long. The full proposal id stays in
`ContinuationState`, `ActionProposal`, `ContinuationLease`, and execution events;
the compact id is transport-only and resolves by comparing against the aliases of
persisted ids.

## Organic proposal sandbox leases

Organic proposal inference may infer a bounded proposal from plan, operation, or continuation
state even when the face model did not emit the explicit proposal contract. That
inference is allowed to create `system_change` proposals, but materialization
adds a sandbox contract before the button can approve execution:

- `ActionProposal` and `ContinuationLease` include
  `execute_in_approved_user_sandbox`.
- System-change leases allow writes only to the approved-user workspace, user
  memory, or `/tmp`; prompt root and shared memory remain read-only.
- Network access, secrets, commit, deploy, restart, and remote push remain
  forbidden unless a separate capability/lease explicitly grants them.
- When the approved continuation turn spawns, runtime executes it under the
  `approved_user` isolated profile even if the approving human is an admin.

The approval still records the original approver. The sandbox role is only the
execution principal for that bounded organic proposal continuation.
