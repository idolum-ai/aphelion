# Architecture Waivers

This file is the single ledger for temporary architecture waivers.

Rules:
- Every waiver must include `owner` and `expires_on`.
- Expired waivers must be removed or renewed in an explicit follow-up commit.
- Waivers are temporary seams, not permanent abstractions.

## Active Waivers

### W-2026-05-runtime-continuation-private-leaf

- `owner`: runtime
- `expires_on`: 2026-08-31
- `scope`: `runtime/continuation/` may exist as a private runtime leaf
  subpackage imported only by top-level `runtime`.
- `rationale`: The current `runtime/continuation_*.go` cluster has outgrown
  flat root-package ownership. On 2026-05-27 it contained 40 files and 13,123
  LOC across continuation leases, approval materialization, operation phase-plan
  projection, parking/recovery, rendering, and work-executor handoff. Keeping
  the seam implicit silently accumulates coupling; extracting the whole cluster in
  one step is too broad for a hardening PR.
- `constraints`:
  - Do not expose `runtime/continuation` as a public package or cross-repository
    API. The architecture import guard must continue to allow only top-level
    `runtime` to import runtime leaf packages.
  - Do not move ingress/session/lifecycle orchestration ownership out of the
    runtime shell; the leaf may own bounded continuation mechanics only.
  - Any extraction PR must preserve existing continuation behavior with focused
    runtime tests and must not broaden authority, deployment, or restart policy.
- `follow_up`: Before expiry, either extract a first bounded slice into
  `runtime/continuation/`, renew this waiver with fresh evidence, or replace it
  with a permanent architecture decision explaining why the cluster must remain
  flat.
