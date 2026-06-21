# Execution Authority Continuity

_Status: current contract plus conformance matrix._

Execution authority continuity is the horizontal contract between an authorized
turn and the tools or resources used during that turn. It exists because a
durable child, operation-plan continuation, recovery turn, or native work
executor may cross several generic runtime layers before a concrete tool
invocation happens.

The invariant is:

> A capability grant is never enough by itself. Every authority-sensitive
> invocation must redeem current durable lease evidence at point of use, then
> record invocation evidence that names the session and lease that authorized
> the turn.

The child substrate is deliberately strong vertically: durable child identity,
policy, workspace, memory, control traffic, replay protection, snapshots, and
reporting are owned by `durableagent/`. This document covers the horizontal
bridge after child or continuation work enters ordinary execution machinery.

## Continuity Spine

The execution-authority spine is:

1. Execution species creates or resumes work under a typed continuation or
   operation-plan lease.
2. Runtime transports an authority-use reference through the internal executor
   boundary.
3. Tool invocation treats that reference as a selector, not proof.
4. Tool invocation revalidates the selected lease against durable session state:
   session binding, source kind, lease ID, status, expiry, and remaining turns.
5. Capability grants are checked for principal, kind, resource, and exact action.
6. Resource authority is compiled for the concrete operation.
7. Invocation evidence records grant, principal, action, session, authority
   source, and lease IDs.

Context may carry an authority-use reference, but it may not manufacture
authority. Durable state remains canonical.

## Effective Authority

For capability-managed tools, the effective authority is:

`principal + current session lease + active grant + exact action + invocation input`

For native file access, the effective authority is:

`sandbox ceiling + current session lease + active file_access grant + exact file operation + requested path`

This is not a blanket widening of native sandbox roots. A `file_access` grant may
add a temporary operation-specific root only after the lease and grant are
validated. Hidden paths remain hidden. Symlink grant roots are rejected so the
authority boundary cannot be retargeted after approval. Missing approved write
roots may be materialized only when the requested path remains under the granted
root.

Narrow file actions stay narrow:

| Grant action | Native operations allowed |
| --- | --- |
| `read` | `read_file`, `list_dir`, `search` |
| `read_file` | `read_file` |
| `list`, `list_dir` | `list_dir` |
| `search` | `search` |
| `inspect` | `list_dir`, `search` |
| `write` | `write_file` |
| `write_file` | `write_file` |

Actions such as `append`, `create`, or `update` do not imply overwrite-capable
`write_file` until a narrower native operation exists.

## Execution Species Matrix

The matrix is intentionally broader than direct tool invocation. Some species
currently do not expose capability-managed tools at all; their continuity test is
that they remain scoped protocol or presentation paths until a future change
explicitly adds a point-of-use gate.

| Species | Entry shape | Authority transport | Point-of-use gate | Current automated coverage |
| --- | --- | --- | --- | --- |
| Interactive tool invocation | User turn through ordinary tool registry | Durable session lookup or explicit context reference | `Registry.authorityUseRefForGrant` plus capability invocation audit | Covered by `TestExecutionAuthorityContinuityToolBoundaryMatrix` durable-fallback row |
| Native continuation | Runtime work executor invoking an internal continuation turn | `AuthorityUseRef` in context | Context reference revalidated against current continuation lease | Covered by continuation-context rows, native file grant tests, and `TestNativeWorkExecutorCarriesAuthorityUseRefIntoInternalTurn` |
| Operation-plan continuation | Runtime work executor under active plan lease | `AuthorityUseRef` in context | Context reference revalidated against current operation-plan lease | Covered by operation-plan context rows and `TestNativeWorkExecutorCarriesAuthorityUseRefIntoInternalTurn` |
| Durable group child | Durable child enters parent runtime/group turn path | Durable-agent scope, child adapter context, no parent tool registry by default | If tools are ever exposed, same lease and grant gate before tool/resource use | Covered by `TestDurableGroupTurnDoesNotExposeParentToolAuthorityByDefault`; group turns currently expose no parent tools |
| Remote child | Remote child reports/requests work through parent control plane | Signed child protocol plus review artifacts/parent conversation sync | If parent-side tools are ever exposed, same lease and grant gate before tool/resource use | Covered by remote child protocol tests; remote child currently uploads review artifacts rather than invoking parent tools |
| Maintenance/recovery | Runtime-synthesized maintenance turn | Durable lookup or runtime authority reference | Expired/revoked/exhausted leases rejected before invocation | Covered by expired/revoked/exhausted rows |
| Scheduled continuation | Runtime re-entry/resume path | Durable lookup or runtime authority reference | Current compatible lease required; stale state remains evidence only | Covered by exhausted/stale-lease rows at tool boundary |
| Scheduled job | Runtime-synthesized scheduled maintenance turn | Dedicated scheduled-job session scope, no inherited chat authority | If tools require grants, they must validate against that scheduled scope | Covered by `TestScheduledJobAuthorityContinuityUsesDedicatedSessionScope` |

## Conformance Cases

Every execution species should be able to demonstrate the following cases at the
boundary where it invokes a capability-managed tool or resource:

| Case | Expected result |
| --- | --- |
| Current continuation lease, matching grant/action/resource | Invocation allowed and audit records session + continuation lease |
| Current operation-plan lease, matching grant/action/resource | Invocation allowed and audit records session + operation-plan lease |
| Missing lease evidence | Invocation blocked |
| Fabricated lease ID | Invocation blocked |
| Wrong session | Invocation blocked |
| Expired lease | Invocation blocked |
| Exhausted lease | Invocation blocked |
| Revoked lease | Invocation blocked |
| Authority source/ID mismatch | Invocation blocked |
| Grant action mismatch | Invocation blocked even with a valid lease |
| Resource path outside effective grant/sandbox policy | Invocation blocked |
| Symlink grant root or hidden path | Invocation blocked |
| Approved missing write root with create operation | Invocation allowed only under that grant root |
| Restart between approval and invocation | Invocation revalidates durable state before acting |
| Species does not expose tools | No parent/admin tool authority is available by resemblance |

## Current Test Anchors

- [`tool/execution_authority_continuity_test.go`](../../tool/execution_authority_continuity_test.go)
- [`tool/authority_access_test.go`](../../tool/authority_access_test.go)
- [`tool/native_file_tools_test.go`](../../tool/native_file_tools_test.go)
- [`runtime/work_executor_test.go`](../../runtime/work_executor_test.go)
- [`runtime/execution_authority_continuity_runtime_test.go`](../../runtime/execution_authority_continuity_runtime_test.go)
- [`durableagent/remote_child_test.go`](../../durableagent/remote_child_test.go)

The first anchor is the conformance matrix seed. The others cover concrete
regressions around context lease evidence, native file grants, runtime
propagation into native work execution, durable group non-tool exposure,
scheduled job scoping, and remote child review-artifact protocol behavior.

## Remaining Integration Debt

The current implementation validates concrete invocations correctly at the
shared tool/file-access boundary. It does not yet expose one canonical
execution-authority envelope type that binds principal, session/scope, run,
lease kind, grant, action, resource, expiry, and source across every execution
species.

Until that envelope exists, new execution species must either:

- reuse the existing authority-use reference path and point-of-use validation; or
- add an equivalent conformance row and tests before invoking
  capability-managed tools or native file resources.

This is monitored architecture debt, not a reason to duplicate authority checks
inside child-specific adapters. The child substrate should remain vertically
bounded; the horizontal bridge belongs at the execution-authority boundary.
