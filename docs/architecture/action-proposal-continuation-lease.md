# ActionProposal and ContinuationLease

`ActionProposal` and `ContinuationLease` are the v1 contract for turning a
model-authored “I can continue / execute this bounded plan” into explicit human
approval without widening authority by vibe.

## Purpose

- `ActionProposal` names the proposed bounded action contract.
- `ContinuationLease` is the consumable authorization derived from an approved
  proposal.
- Approval callbacks carry proposal/lease identity, not freeform authority.
- The lease is consumed, revoked, or expired by runtime state; it does not grant
  new tools, accounts, devices, purchases, or public effects by itself.

## Current Storage

For v1, both records are embedded in
`session.ContinuationState` / `sessions.continuation_state_json`.

This keeps the current Telegram `Continue` / `Stop` flow intact while making the
implicit continuation prompt contract typed and auditable.

## ActionProposal Fields

The typed record includes:

- `id`
- `operation_id`
- `mission_id`
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
is pending until the user presses `Continue`, then approved if still fresh.

## ContinuationLease Fields

The typed record includes:

- `id`
- `proposal_id`
- `mission_id`
- `status`
- `max_turns`
- `remaining_turns`
- `approved_by`
- `allowed_actions`
- `forbidden_actions`
- `validation_plan`
- `expires_at`
- `plan_hash`
- lifecycle timestamps

The current v1 lease is one-turn by default with a short TTL. On trigger, stale
leases expire closed. On execution, the lease consumes a turn before the
machine-authored continuation event is processed.

## Lifecycle

1. A turn produces both persona and governor continuation intent.
2. If both are eligible and governor-ratified, runtime creates:
   - pending `ActionProposal`
   - pending `ContinuationLease`
   - pending continuation state
3. Telegram renders a `Continue` / `Stop` button pair.
4. `Continue` approves the proposal and activates the lease.
5. Runtime triggers one machine-authored continuation turn.
6. Consuming the turn decrements the lease; zero remaining turns marks it
   consumed and returns continuation state to idle.
7. `Stop` revokes the continuation and lease.
8. Expired proposals/leases fail closed and emit `continuation.blocked`.

## Non-Authority Rule

A `ContinuationLease` authorizes only continuation under existing authority and
bounded effect. Any expanded capability still goes through the capability
request/review/grant lane.

## Code Anchors

- [`session/types.go`](../../session/types.go)
- [`session/store.go`](../../session/store.go)
- [`runtime/continuation.go`](../../runtime/continuation.go)
- [`runtime/runtime.go`](../../runtime/runtime.go)
- [`commands_continuation.go`](../../commands_continuation.go)


## Generic ActionProposal UI v1

The first generic UI surface is mission-review backed:

- `/mission propose <mission_id>` renders an `ActionProposal` with Telegram
  `Deny`, `Ask edit`, and `Approve` buttons.
- Callback data is keyed by the proposal id (`action_proposal:<proposal_id>:<action>`).
- For mission-backed proposals the proposal id currently derives from the
  mission id (`aprop-<mission_id>`).
- `Approve` marks a candidate/dormant mission active for review/planning only.
- `Ask edit` leaves the mission candidate and records `waiting_for=proposal_edit`.
- `Deny` leaves the mission review-only and records `waiting_for=proposal_denied`.

This v1 UI is intentionally not a tool execution grant and does not create a
self-continuation lease. It makes the approval control surface real while
keeping actual execution authority in later, bounded ActionProposal or
ContinuationLease requests.


## Mission Review Proposal Card

Mission proposal cards are pre-ActionProposal intake gates. They let the
system suggest a candidate mission with inline buttons before the mission exists
in the ledger.

Buttons:

- `Add mission`: create a candidate mission with default review-only
  authority.
- `Ask edit`: request a revised proposal; no mission is created.
- `Park`: leave the idea untracked for now; no mission is created.
- `Reject`: reject the idea; no mission is created.

This card deliberately does not authorize execution. It only decides whether an
idea becomes a candidate mission that may later receive an ActionProposal /
ContinuationLease.

## Continuation controls v2

Continuation prompts now use short explicit lease-control buttons instead of a
single ambiguous `Continue` button. Newly rendered Telegram labels are:

- `Start`: approve the pending ContinuationLease exactly as written and trigger
  the bounded continuation.
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
