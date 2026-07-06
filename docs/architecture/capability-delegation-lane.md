# Capability Delegation Lane

This document defines the general capability request and delegation contract used
when a child, tenant, or agent needs permission beyond its current envelope.

The lane covers tools, local-device access, external accounts, purchases, public
web interaction, communication surfaces, file/network access, and emergent
permissions that do not deserve a one-off governance path.

## Canonical Flow

The canonical flow is:

`request -> classify -> review -> provision -> attest -> grant -> expose/invoke -> observe -> renew/revoke`

- `request`: an authenticated principal submits a `capability_request` with
  requester attribution, target principal, capability kind, target resource,
  purpose, risk class, proposed contract, and constraints.
  When immediate operator visibility is needed, the request may include a
  `review_target_chat_id` and optional `review_summary`; Telegram-scoped turns
  default the review target to the current chat when no explicit target is
  supplied. This queues a pending review event without changing approval or
  grant semantics. Headless or non-Telegram submissions remain ledger-only
  unless they name a review target. Requests may also embed a
  `capability_update_plan` in the contract when approval should result in a
  concrete downstream change such as a durable child policy patch.
- `classify`: the request is normalized into one capability kind:
  `tool`, `local_device`, `external_account`, `purchase`, `public_web`,
  `communication`, `file_access`, `network_access`, or `generic_delegation`.
- `review`: parent principals may endorse or reject requests that name them;
  admins perform final approval or rejection. Requests that name a parent must
  reach `parent_approved` before an admin can approve them.
- `provision`: any setup work happens outside the request itself. For tools,
  this is still the external-tool install/audit/probe/register lifecycle.
- `attest`: the operator records the evidence appropriate for the capability.
  Tool capabilities use the external-tool audit/probe attestation contract.
  Other capabilities use the grant contract and policy hash as their baseline.
- `grant`: an admin creates or updates a `capability_grant` with granted
  principal, allowed actions, contract, constraints, status, policy fingerprint,
  expiration, and stale/revocation state. If an active grant carries a durable
  child `capability_update_plan`, the grant is staged as pending, the live
  policy patch is applied under the child bootstrap ceiling, and the grant only
  becomes active after that policy update succeeds.
- `expose/invoke`: runtime access checks require an active unexpired grant for
  the requested action. For `kind=tool`, an active grant with `invoke`
  authorizes a registered tool; there is no separate tool-exposure authority
  surface.
- `observe`: invocations and checks can update invocation/failure counters and
  last-used timestamps.
- `renew/revoke`: admins may revoke grants, expire them, or replace them with a
  fresh grant when policy or environmental assumptions drift.

## Canonical State

The source of truth is SQLite session state:

- `capability_requests`: requested capability, target principal, purpose,
  risk class, contract, constraints, review status, and linked grant.
- `capability_reviews`: append-only review decisions with reviewer attribution.
- `capability_grants`: granted principal, kind, target resource, allowed
  actions, contract, constraints, policy hash/fingerprint, status, stale reason,
  and counters.
- `capability_invocations`: invocation-level audit trail for grant use,
  including the session and continuation or operation-plan lease that made this
  turn authorized.

`capability_update_plan` is not a separate authority surface. It is an optional
contract field on a request or grant. Current durable-child plans support:

- `agent_id`: durable child receiving the policy update.
- `policy_patch`: high-level child policy changes such as autonomy, visibility,
  shared context, capabilities, charter, and drift policy.
- `policy_overrides`: explicit low-level live-policy axes when needed.
- `provisioning` and `attestation`: operator-visible setup and evidence steps.
- `grant_actions`: suggested grant actions; used as the grant default when the
  admin does not provide `allowed_actions`.
- `reason` and `notes`: provenance and operator context.

`durable_agent delegation_request` is the durable-child capability request path.
It creates the same canonical `capability_requests` row, attributes the
request to the child by default, derives parent/admin principals when possible,
queues a durable review artifact for the operator, and can embed a
`capability_update_plan` produced by the same conversation. This is the preferred
path when a child-agent conversation discovers an emergent need such as local
device access, external account access, a purchase, public web exposure, file or
network expansion, or another permission that should not become a new bespoke
durable action.

`durable_agent delegation_report` queues a durable review artifact tied to an
existing request or grant. It is for progress, blocked-state, outcome, and risk
reports; it does not itself approve, grant, revoke, or invoke capability.

## Authority Rules

- Any authenticated child, tenant, approved user, durable agent, or admin may
  submit and inspect visible requests through `capability_request`.
- Direct `capability_request` review notifications are only notifications: they
  do not skip parent review, admin approval, provisioning, attestation, grant,
  or access checks.
- Admins may submit `durable_agent delegation_request` on behalf of a durable
  child when the request emerged through that child conversation. The resulting
  row is still reviewed and granted through `capability_authority`.
- A principal can see a request when it is the requester, requested target,
  named parent, named admin, or an admin actor.
- `capability_authority request_review parent_approved` is allowed to the named
  parent principal or an admin.
- `capability_authority request_review approved` is admin-only and requires
  `parent_approved` first when a parent principal is named.
- `capability_authority grant_set` and `grant_revoke` are admin-only.
- `capability_authority grant_set` applies durable-child
  `capability_update_plan` policy patches only for active grants. Bootstrap
  ceiling violations fail the grant instead of silently widening a child.
- Grant visibility follows granted-to, granted-by, related request visibility,
  or admin role.

## Status Projection

`/status` and `_show`-style readouts project these canonical surfaces as:

- `capability_requests source=canonical:session.capability_requests`
- `capability_grants source=canonical:session.capability_grants`
- `capability_lifecycle source=canonical:execution_events.capability_delegation`

The projection includes request status, parent/admin attribution, grant status,
allowed actions, stale reasons, drift source, policy anchor, invocation counters,
and failure counters.
