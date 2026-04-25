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
  expiration, and stale/revocation state.
- `expose/invoke`: runtime access checks require an active unexpired grant for
  the requested action. For `kind=tool`, an active grant with `invoke` can
  authorize a registered tool even without a legacy `tool_exposure`.
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
- `capability_invocations`: invocation-level audit trail for grant use.

`tool_request` is now compatibility input for `kind=tool`: it still creates the
legacy `tool_proposals` row, and also writes the canonical `capability_requests`
row. New broad permissions should use `capability_request` directly.

## Authority Rules

- Any authenticated child, tenant, approved user, durable agent, or admin may
  submit and inspect visible requests through `capability_request`.
- A principal can see a request when it is the requester, requested target,
  named parent, named admin, or an admin actor.
- `capability_authority request_review parent_approved` is allowed to the named
  parent principal or an admin.
- `capability_authority request_review approved` is admin-only and requires
  `parent_approved` first when a parent principal is named.
- `capability_authority grant_set` and `grant_revoke` are admin-only.
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
