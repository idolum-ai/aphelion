# Agent Authority Ledger

_Status: draft architecture spec._

This document defines the authority contract Aphelion should converge on for
agent action, operator consent, continuation, auto-approval, durable children,
restart recovery, and external effects.

The short rule is:

> The ledger is the source of truth. Text is presentation.

The system may render friendly text, concise buttons, persona phrasing, status
summaries, and doctor output, but those surfaces must project ledger records.
They must not become independent authority by string matching, phrase detection,
or hidden prompt convention.

## Purpose

Aphelion exists as a live personal-agent runtime, not only a chat interface. It
therefore needs a durable answer to these questions:

- What action is being proposed?
- Who is allowed to approve it?
- Which subject's consent is required?
- What exact authority was granted?
- What remains forbidden?
- When does the authority expire?
- How many turns or actions may consume it?
- What evidence proves the action happened, failed, blocked, or was skipped?
- What should happen after restart, deploy, provider failure, or stale callback?

Today, parts of this answer live in continuation state, operation plans,
capability grants, mission state, Telegram buttons, execution events, status
projections, and model-authored language. The Agent Authority Ledger names the
shared contract those pieces should implement.

## Design Principles

- Authority is typed, not inferred from prose.
- Text can request, explain, summarize, and render authority, but cannot itself
  grant authority.
- Approval is scoped by subject, actor, action, boundary, expiration, and
  evidence requirement.
- Revocation, expiry, denial, consumption, and completion are first-class states.
- Auto-approval is a lease with a scope and TTL, not a global mood.
- External effects require sharper authority than local analysis.
- Private data and third-party consent are distinct from operator convenience.
- Restart and deploy must preserve the ledger before interrupting work.
- The persona and governor may argue about authority, but the runtime stores the
  resulting contract.
- `/status`, `/debug`, `/doctor`, and Telegram buttons are projections over the
  ledger and TES, not separate truth sources.

## Truth Classes

This spec follows the architecture truth classes in
[`README.md`](README.md).

- `canonical`: append-only authority events and execution evidence.
- `operational current-state store`: mutable current proposal, lease, mission,
  plan, grant, and auto-approval state used by runtime checks.
- `projection`: Telegram cards, buttons, `/status`, `/debug`, `/doctor`,
  quick-read summaries, and logs.
- `compatibility fallback`: legacy rows or sidecars used only while migration is
  incomplete.

The target shape is:

- TES is canonical for lifecycle facts.
- Ledger state is the operational current-state store for currently usable or
  pending authority.
- User-facing text is a projection.

## Ledger Concepts

### Principal

A `principal` is an entity that can request, approve, receive, consume, or be
protected by authority.

Examples:

- admin operator
- approved Telegram user
- durable child agent
- parent principal
- resource owner
- external account owner
- third-party person
- system service principal

Principals should be explicit in ledger records whenever an action crosses a
trust boundary.

### Consent Subject

The consent subject is the party whose consent is semantically required. This is
separate from the operator who clicks a button.

Canonical subject classes:

- `operator`: the current operator may approve the action for their own runtime.
- `admin`: only an admin may approve because system authority is affected.
- `resource_owner`: the owner of private data, account access, or personal
  material must consent.
- `third_party`: another person must opt in before Aphelion may act on or
  contact them.
- `parent_principal`: a parent principal must endorse child expansion before
  admin approval.
- `system`: no human consent can override a hard runtime invariant.

Examples:

- Reading local non-secret repo files: `operator`.
- Restarting the live service: `admin`.
- Reading a spouse's CV or mailbox: `resource_owner`.
- Contacting someone publicly: `third_party` or `resource_owner`, depending on
  whose behalf the action is taken.
- A durable child requesting external account access: `parent_principal` plus
  `admin`.
- Hardline destructive filesystem action: `system`.

### Authority Scope

Scope names what the approval can affect.

Recommended scope classes:

- `status_check`: inspect already-safe runtime state.
- `read_only_review`: read local non-secret state and report.
- `local_workspace`: mutate approved local workspace files.
- `workspace_commit`: create local commits in the approved repo.
- `repo_push`: push to a remote repository.
- `deploy_restart`: install, restart, or deploy the live service.
- `private_data_intake`: ingest user-provided private data.
- `external_account_read`: read an external account or mailbox.
- `external_account_write`: mutate an external account.
- `public_web_read`: fetch public web resources.
- `public_contact`: send messages, submit forms, apply to jobs, post publicly,
  or contact a person/entity.
- `purchase_or_payment`: spend money or create financial commitments.
- `child_wake`: wake or route a durable child.
- `capability_grant`: expose or modify durable capability grants.
- `network_surface`: create, expose, or revoke private network surfaces.

Scopes should be stable enough for code, logs, and tests, but rendered in human
language for operators.

### Authority Class

Authority class summarizes operational risk for a proposal or lane.

Current implementation names already include:

- `local_workspace`
- `data_access`
- `child_wake`
- `capability_grant`
- `deploy_restart`

The ledger should converge authority class and scope classification so UI labels
and runtime checks do not drift.

### Bounded Effect

The bounded effect is the maximum action the lease permits. It must be specific
enough to stop execution when the model tries to widen the task.

A good bounded effect includes:

- allowed resource or workspace
- explicit non-goals
- forbidden external effects
- validation gates
- stop conditions
- maximum turns or actions

### Evidence Reference

An evidence reference points to a TES event, artifact, file diff, command result,
provider failure record, delivery record, or other runtime-observable fact.

Claims about work should cite evidence references internally even when the
rendered message stays short.

## Core Records

The first ledger can be implemented by normalizing existing records instead of
adding a new monolithic table immediately.

### ActionProposal

An `ActionProposal` is a pending request for bounded authority.

Current code anchor: [`session/types.go`](../../session/types.go)
`ActionProposal`.

Required semantic fields:

```json
{
  "id": "aprop-mada-job-agent-j1",
  "kind": "action_proposal",
  "summary": "Plan: Mada's Job Agent (Phase J1)",
  "why_now": "The user asked to inspect the local job-agent plan and prepare the next bounded step.",
  "bounded_effect": "Read local non-secret repo/session state and produce a plan. Do not read mailbox contents, use credentials, edit files, commit, deploy, restart, send messages, or contact anyone.",
  "authority_scope": ["read_only_review"],
  "consent_subjects": ["operator"],
  "requested_by": "persona:idolum",
  "requested_for": "operator:telegram:6313146",
  "risk_class": "low",
  "allowed_actions": [
    "inspect_local_non_secret_state",
    "summarize_findings",
    "propose_next_lease"
  ],
  "forbidden_actions": [
    "read_secret_values",
    "read_mailbox_contents",
    "edit_files",
    "commit",
    "deploy",
    "restart",
    "public_contact"
  ],
  "validation_plan": [
    "cite local evidence",
    "stop before any escalation"
  ],
  "plan_hash": "sha256:...",
  "status": "pending",
  "expires_at": "2026-05-07T21:30:00Z"
}
```

Current implementation fields cover most of this, but consent subjects and
authority scopes are still partly implicit in allowed/forbidden actions and risk
classification.

### ContinuationLease

A `ContinuationLease` is consumable authority derived from an approved proposal.

Current code anchor: [`session/types.go`](../../session/types.go)
`ContinuationLease`.

```json
{
  "id": "lease-mada-job-agent-j1",
  "kind": "continuation_lease",
  "proposal_id": "aprop-mada-job-agent-j1",
  "status": "active",
  "lease_class": "local_workspace",
  "approved_by": "telegram_user:6313146",
  "approved_at": "2026-05-07T21:21:00Z",
  "max_turns": 1,
  "remaining_turns": 1,
  "constraints": {
    "externality": "no deploy, restart, credentials, purchases, public contact, or external accounts",
    "scope": "local repo/workspace only",
    "validation": "focused evidence before report"
  },
  "allowed_actions": [
    "inspect_local_non_secret_state",
    "summarize_findings"
  ],
  "forbidden_actions": [
    "read_secret_values",
    "read_mailbox_contents",
    "edit_files",
    "deploy",
    "restart",
    "public_contact"
  ],
  "expires_at": "2026-05-07T22:21:00Z",
  "plan_hash": "sha256:..."
}
```

Lease states:

- `pending`: proposal exists but is not yet approved.
- `active`: usable authority exists.
- `consumed`: all permitted turns/actions were spent.
- `revoked`: user or runtime intentionally stopped it.
- `expired`: time or freshness limit elapsed.
- `blocked`: execution attempted but current checks denied access.
- `completed`: bounded objective finished and evidence was recorded.

The current enum does not include `blocked` or `completed` for continuation
leases. Those states are represented elsewhere and should be normalized.

### OperationPlanLease

An `OperationPlanLease` is a budget for a broader plan. It should replace
repeated phase-by-phase ritual when the operator already approved the disclosed
budget.

Current code anchor: [`session/types.go`](../../session/types.go)
`OperationPlanLease`.

```json
{
  "id": "planlease-mada-job-agent-j",
  "kind": "operation_plan_lease",
  "summary": "Plan: Mada's Job Agent",
  "objective": "Design and validate the consent-first job-agent workflow through a local repo implementation plan.",
  "status": "active",
  "turn_budget": 6,
  "remaining_turns": 4,
  "covered_phase_ids": ["j1", "j2", "j3"],
  "lanes": [
    {
      "id": "j1",
      "summary": "Read-only diagnosis",
      "authority_class": "read_only_review",
      "expected_turns": 1
    },
    {
      "id": "j2",
      "summary": "Local implementation",
      "authority_class": "local_workspace",
      "expected_turns": 3
    },
    {
      "id": "j3",
      "summary": "Focused validation",
      "authority_class": "local_workspace",
      "expected_turns": 2
    }
  ],
  "hard_interrupts": [
    "needs credentials",
    "needs private mailbox contents",
    "needs third-party opt-in",
    "needs deploy/restart",
    "needs public contact"
  ],
  "validation_gates": [
    "targeted tests",
    "go test ./...",
    "git diff --check"
  ],
  "exit_conditions": [
    "implementation committed",
    "validation reported",
    "next live-test ask prepared"
  ]
}
```

The plan lease allows Aphelion to ask for a readable budget once, then continue
inside that budget without asking for every disclosed phase. It must still stop
and escalate when a hard interrupt appears.

### AutoApprovalLease

An auto-approval lease is bounded ambient authority for a chat, operator,
principal, or scope.

This should be represented as a ledger lease, not merely a config flag or
boolean runtime setting.

```json
{
  "id": "auto-6313146-20260507T2100Z",
  "kind": "auto_approval_lease",
  "status": "active",
  "approved_by": "telegram_user:6313146",
  "chat_id": 6313146,
  "scope": ["read_only_review", "local_workspace"],
  "exclusions": [
    "private_data_intake",
    "external_account_read",
    "external_account_write",
    "public_contact",
    "purchase_or_payment",
    "deploy_restart",
    "repo_push",
    "third_party_consent"
  ],
  "expires_at": "2026-05-07T22:00:00Z",
  "max_duration": "48h",
  "consumption_policy": "record_each_auto_approval"
}
```

Auto-approval should never bypass:

- explicit third-party opt-in
- resource-owner consent
- hardline runtime invariants
- undisclosed deploy/restart or push
- private data access not included in scope
- payment or public contact unless explicitly scoped

When auto-approval applies, runtime should materialize a normal approval event
that cites the auto-approval lease as cause. Execution should not proceed from a
hidden branch that leaves no approval record.

### CapabilityGrant

A `CapabilityGrant` is standing authority for a principal or child to use a
capability.

Current code anchors:

- [`capability-delegation-lane.md`](capability-delegation-lane.md)
- [`session/types.go`](../../session/types.go) `CapabilityRequest`,
  `CapabilityReview`, and `CapabilityGrant`

Capability grants are separate from continuation leases:

- A continuation lease authorizes a bounded run or plan step.
- A capability grant exposes a reusable capability under constraints.

A turn may need both. For example, a child may have an active `external_account`
grant, but the current turn still needs a bounded lease to use it for a specific
purpose.

### AuthorityEvent

An `AuthorityEvent` is the append-only fact that something happened to proposal,
lease, grant, plan, or auto-approval state.

Current implementation should map these to TES event families such as:

- `decision.opened`
- `decision.resolved`
- `decision.expired`
- `continuation.offered`
- `continuation.approved`
- `continuation.revoked`
- `continuation.consumed`
- `continuation.blocked`
- `recovery.issued`
- `recovery.completed`
- capability delegation events

Target event shape:

```json
{
  "event_type": "authority.approved",
  "stage": "decision",
  "status": "resolved",
  "subject_id": "lease-mada-job-agent-j1",
  "subject_kind": "continuation_lease",
  "actor": "telegram_user:6313146",
  "caused_by": "telegram_callback:approve_lease",
  "decision_id": "decision-mada-job-agent-j1",
  "evidence_refs": ["tes:6313146:1287"],
  "created_at": "2026-05-07T21:21:00Z"
}
```

The first implementation can keep using existing TES event names, but payloads
should carry enough authority metadata for projections to avoid reading prose.

## Lifecycle

### Proposal to Completion

```text
intent observed
  -> proposal drafted
  -> proposal classified
  -> consent subjects resolved
  -> operator card rendered
  -> approval, denial, ask-edit, expiry, or auto-approval
  -> lease activated
  -> turn/action consumes lease
  -> execution evidence recorded
  -> completion, blocked, failure, or next proposal recorded
  -> projection rendered
```

### Expired Callback

When a user clicks an expired approval button:

1. Runtime records `authority.callback_stale` or the current TES equivalent.
2. The old proposal/lease remains expired.
3. Runtime creates a fresh proposal for the same bounded action if current state
   still supports it.
4. The user sees the fresh proposal, not a dead-end stale message.

### Verbal Approval

When the user replies "approved" or equivalent:

1. Runtime should not execute directly from the text.
2. Runtime should resolve the pending decision or blocked phase.
3. If the text is accepted as consent, runtime must materialize the same
   approval/lease event that a button would have created.
4. Execution consumes that materialized lease.
5. If no matching pending decision exists, runtime should ask for a concrete
   proposal instead of claiming approval.

### Unexpected Authority

When execution discovers it needs authority outside the lease:

1. Stop before the action.
2. Record `authority.escalation_needed` or equivalent evidence.
3. Create a narrower proposal for the newly needed authority.
4. Explain only the delta in the user-facing card.
5. Resume the old plan only after the new authority is granted, if the old lease
   still exists and remains valid.

### Revocation

Revocation stops pending or active authority. The projection should name the
human plan title, not the raw lease id.

Example projection:

```text
Stopped Plan: Mada's Job Agent (Phase J1)
```

The ledger still stores the full lease id, proposal id, actor, timestamp, and
causal event.

### Restart and Deploy

Before a live restart or deploy:

1. Park active turns and leases.
2. Record handoff evidence and restart intent.
3. Stop accepting new execution under leases that cannot survive restart.
4. After startup, reconcile:
   - active turns
   - parked leases
   - pending proposals
   - auto-approval leases
   - child wakes
   - recovery rows
5. Re-offer, resume, or expire each item based on ledger state.

The startup message should be a projection over this reconciliation, not a fixed
template.

## Persona, Governor, and Runtime Roles

The persona is allowed to be an interpretive participant. It may notice missing
context, ask for context, render a warmer card, or argue that a proposal is too
narrow.

The governor is allowed to classify authority, identify missing consent, and
ratify or reject proposed action.

The runtime is responsible for:

- storing the proposal/lease/grant state
- enforcing access checks
- consuming leases
- recording TES evidence
- refusing stale or widened authority
- recovering after restart

No role should depend on magic phrases such as "I need to correct that",
"sending work evidence", "approved", or "destructive change" as the durable
contract. Those phrases may appear in text, but they are never the source of
truth.

## User Interface Rules

Operator UI should be readable and short, while preserving machine precision in
ledger state.

### Proposal Card

Recommended default shape:

```text
Approval needed.

Plan: Mada's Job Agent (Phase J1)

Why now:
Inspect the local plan and prepare the next bounded implementation step.

Allowed:
Read local non-secret repo/session state and summarize findings.

Stops before:
Credentials, mailbox contents, file edits, commit, deploy, restart, or contact.

Approve 1 turn?
```

Buttons should carry proposal/lease identity, not authority text.

### Status

`/status` should answer:

- Is work running, blocked, idle, or waiting?
- What plan or lease is active in human terms?
- What approval budget exists?
- What is the next action?
- What evidence supports the claim?
- What stale or orphaned items need cleanup?

### Doctor

`/doctor` should answer:

- Is ledger state internally consistent?
- Are there active leases with no proposals?
- Are there pending proposals with no renderable decision?
- Are there expired auto-approval leases still being used?
- Are continuation states and TES events contradictory?
- Are parked leases recoverable?
- Are durable child grants stale or drifted?

## Current Implementation Mapping

Current Aphelion pieces that already implement parts of the ledger:

- `ActionProposal` and `ContinuationLease` in
  [`session/types.go`](../../session/types.go)
- operation phase and plan leases in
  [`session/types.go`](../../session/types.go)
- capability delegation request/review/grant rows in
  [`capability-delegation-lane.md`](capability-delegation-lane.md)
- mission state and authority budget draft in
  [`mission-ledger-roadmap.md`](mission-ledger-roadmap.md)
- TES authority event families in
  [`transparent-execution-sequence.md`](transparent-execution-sequence.md)
- Telegram continuation controls in
  [`action-proposal-continuation-lease.md`](action-proposal-continuation-lease.md)
- status and debug projections in
  [`telegram-ui-features.md`](../telegram-ui-features.md)

## Known Gaps

These are expected migration targets, not accusations against the current code.

- Consent subjects are not yet first-class on every proposal, lease, grant, and
  auto-approval record.
- Scope classification and lease class overlap but are not unified.
- `blocked` and `completed` authority states are represented inconsistently
  across continuations, plan leases, and TES.
- Some proposal summaries still derive from model text instead of structured
  proposal fields.
- Some UI labels are still risk-label projections rather than clear capability
  descriptions such as "may delete" or "may restart".
- Auto-approval should be represented uniformly as a ledger lease with
  consumption events.
- Verbal approvals and button approvals should materialize identical ledger
  events.
- Restart recovery should reconcile all active authority classes through one
  ledger projection.
- Durable child capability grants and turn-level leases should cross-reference
  each other when both are required.
- `/doctor` should include ledger consistency checks.

## Migration Plan

### Phase 1: Write Through Existing Records

- Keep existing storage tables and JSON fields.
- Add missing structured fields where low-risk:
  - `authority_scope`
  - `consent_subjects`
  - `requested_by`
  - `requested_for`
  - `evidence_refs`
- Ensure button and verbal approval paths write equivalent TES events.
- Ensure auto-approval writes explicit approval events that cite the
  auto-approval lease.

### Phase 2: Ledger Projection Layer

- Add a read model that projects current authority state from existing
  continuation, plan, capability, mission, auto-approval, and TES sources.
- Use that projection for `/status`, `/debug`, `/doctor`, startup recovery, and
  proposal rendering.
- Mark any compatibility fallback source in the projection.

### Phase 3: Consistency Checks

- Add doctor checks for:
  - active lease without active proposal
  - expired lease still consumable
  - pending proposal with missing decision surface
  - auto-approval applied outside scope
  - capability grant used without turn lease where required
  - blocked phase with no escalation proposal
  - parked lease not considered during startup recovery

### Phase 4: Normalize States

- Converge continuation, plan, grant, and auto-approval states around:
  `pending`, `approved`, `active`, `paused`, `blocked`, `consumed`,
  `completed`, `revoked`, `expired`, and `failed`.
- Keep legacy state names as compatibility decode only until all projections use
  the normalized layer.

### Phase 5: Optional Dedicated Ledger Table

Only add a dedicated `authority_ledger` table if the projection layer proves
that cross-record reads are too fragile or expensive.

The table should be append-only or event-sourced. It should not replace TES; it
should index authority lifecycle facts while TES remains the execution-sequence
truth.

## Validation Gates

Changes that affect authority should include focused tests for:

- button approval materializes a lease
- verbal approval materializes the same lease semantics
- expired callbacks create a fresh proposal when appropriate
- auto-approval applies only inside scope
- private data and third-party consent are not bypassed by auto-approval
- deploy/restart parks and resumes or re-offers leases
- `/status` and `/doctor` do not report authority from presentation text alone
- provider failure/fallback does not consume authority twice
- revocation stops execution and renders the human plan title
- capability grants do not silently widen turn-level leases

Release validation should include:

- `go test ./...`
- `git diff --check`
- a live dry-run or smoke path for continuation approval
- startup recovery verification after reinstall/restart

## Non-Goals

- Do not create a general policy language before current leases and grants are
  normalized.
- Do not force every harmless reply into a proposal.
- Do not make the governor render every safety rationale to the user.
- Do not expose raw lease ids as the primary user-facing handle.
- Do not replace TES with an authority table.
- Do not make the persona a passive skin. It can reason and ask for context, but
  the runtime must store the agreed contract.

## Code Anchors

- [`session/types.go`](../../session/types.go)
- [`session/store.go`](../../session/store.go)
- [`runtime/continuation.go`](../../runtime/continuation.go)
- [`runtime/continuation_materialize.go`](../../runtime/continuation_materialize.go)
- [`runtime/operation_phase_gate.go`](../../runtime/operation_phase_gate.go)
- [`runtime/typed_continuation_approval.go`](../../runtime/typed_continuation_approval.go)
- [`runtime/execution_events.go`](../../runtime/execution_events.go)
- [`runtime/status.go`](../../runtime/status.go)
- [`runtime/capability_authority.go`](../../runtime/capability_authority.go)
