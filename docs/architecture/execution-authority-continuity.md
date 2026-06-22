# Execution Authority Continuity

_Status: implemented contract plus conformance matrix._

Execution authority continuity is the horizontal contract between an authorized
turn and the tools or resources used during that turn. It exists because a
durable child, operation-plan continuation, recovery turn, or native work
executor may cross several generic runtime layers before a concrete tool
invocation happens.

The invariant is:

> A capability grant is never enough by itself. Every authority-sensitive
> invocation must redeem durable run authority at point of use, then record
> invocation evidence that names the run, session, lease, grant, action, and
> outcome.

The child substrate is deliberately strong vertically: durable child identity,
policy, workspace, memory, control traffic, replay protection, snapshots, and
reporting are owned by `durableagent/`. This document covers the horizontal
bridge after child or continuation work enters ordinary execution machinery.

## Continuity Spine

The execution-authority spine is:

1. Execution species creates or resumes work under a typed continuation or
   operation-plan lease.
2. Runtime admits that work into `session.execution_run_authority`, binding the
   turn run to exactly one causal lease kind and ID, the principal, session,
   execution species, and the lease state observed at admission. Admission is a
   transactional claim: another running run cannot claim the same lease, and a
   single-turn lease cannot be rebound later from a stale admission snapshot.
3. Runtime context may carry only the durable run identity
   (`session_id + turn_run_id`). It must not carry a reconstructible assertion
   such as "this lease ID authorizes me."
4. Tool invocation reloads the run authority and revalidates it against current
   durable state: session binding, principal, lease kind, lease ID, revocation,
   expiry, and run/lease compatibility.
5. Capability grants are checked for principal, kind, resource, and exact action.
6. Resource authority is compiled for the concrete operation.
7. Invocation evidence records grant, principal, action, session, turn run,
   authority source, lease IDs, the authorization decision, and the operation
   outcome when the invocation crosses a capability or `file_access` grant.

Context may select durable run authority, but it may not manufacture authority.
Durable state remains canonical.

The distinction between an active lease and a lease spent into a run is
intentional. A continuation can consume its last turn while admitting the work.
That consumed lease remains valid only for the specific `execution_run_authority`
record that captured it while active. Revoked, expired, mismatched, or
session-incompatible leases still fail closed at point of use.

## Effective Authority

For capability-managed tools, the effective authority is:

`principal + durable run authority + current lease state + active grant + exact action + invocation input`

For native file access, the effective authority is:

`sandbox ceiling + durable run authority + current lease state + active file_access grant + exact file operation + requested path`

This is not a blanket widening of native sandbox roots. A `file_access` grant may
add a temporary operation-specific root only after durable run authority, the
current lease, and the selected grant are validated. Hidden paths remain hidden.
Grant roots containing symlink components are rejected so the authority boundary
cannot be retargeted after approval. Missing approved write roots may be
materialized only when the requested path remains under the granted root.

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

This is boundary-level conformance, not proof that every execution species has a
full end-to-end tool flow. Rows marked as non-tool or protocol coverage must not
be cited as evidence that a child, recovery, or scheduled path can invoke a
parent tool safely. They only certify that the current implementation either
reaches the shared point-of-use gate or does not expose that authority surface.

| Species | Entry shape | Authority transport | Point-of-use gate | Current automated coverage |
| --- | --- | --- | --- | --- |
| Interactive tool invocation | User turn through ordinary tool registry | Turn admission creates durable run authority before tool execution; direct tool APIs must receive an existing run identity and do not search the session for leases | Run authority is reloaded and current lease/grant/action are checked before invocation | Covered by `TestExecutionAuthorityContinuityToolBoundaryMatrix`, `TestAuthorityManagedToolDoesNotMintRunAuthorityFromAmbientSessionLease`, and direct tool tests |
| Native continuation | Runtime work executor invoking an internal continuation turn | Pending admission crosses the executor boundary; turn monitor commits `execution_run_authority`; downstream context carries run identity only | Run authority is reloaded and current continuation lease compatibility is checked before invocation | Covered by continuation-context rows, native file grant tests, and `TestNativeWorkExecutorCarriesAuthorityAdmissionIntoInternalTurn`; full native-work-to-tool restart flow remains monitored debt |
| Operation-plan continuation | Runtime work executor under active plan lease | Pending admission crosses the executor boundary; turn monitor commits `execution_run_authority`; downstream context carries run identity only | Run authority is reloaded and current operation-plan lease compatibility is checked before invocation | Covered by operation-plan context rows and native work admission tests |
| Durable group child | Durable child enters parent runtime/group turn path | Durable-agent scope, child adapter context, no parent tool registry by default | If tools are ever exposed, same lease and grant gate before tool/resource use | Covered by `TestDurableGroupTurnDoesNotExposeParentToolAuthorityByDefault`; group turns currently expose no parent tools |
| Remote child | Remote child reports/requests work through parent control plane | Signed child protocol plus review artifacts/parent conversation sync | If parent-side tools are ever exposed, same lease and grant gate before tool/resource use | Covered by remote child protocol tests; remote child currently uploads review artifacts rather than invoking parent tools |
| Maintenance/recovery | Runtime-synthesized maintenance turn | Durable run authority or no authority-sensitive tools | Expired/revoked/exhausted leases rejected before invocation | Covered by expired/revoked/exhausted rows |
| Scheduled continuation | Runtime re-entry/resume path | Durable run authority or no authority-sensitive tools | Current compatible lease required; stale state remains evidence only | Covered by exhausted/stale-lease rows at tool boundary |
| Scheduled job | Runtime-synthesized scheduled maintenance turn | Dedicated scheduled-job session scope, no inherited chat authority | If tools require grants, they must validate against that scheduled scope | Covered by `TestScheduledJobAuthorityContinuityUsesDedicatedSessionScope` |

## Conformance Cases

Every execution species should be able to demonstrate the following cases at the
boundary where it invokes a capability-managed tool or resource:

| Case | Expected result |
| --- | --- |
| Current continuation run authority, matching grant/action/resource | Invocation allowed and audit records turn run + session + continuation lease |
| Current operation-plan run authority, matching grant/action/resource | Invocation allowed and audit records turn run + session + operation-plan lease |
| Missing run authority | Invocation blocked |
| Ambient active lease but no admitted run authority | Invocation blocked and no synthetic turn run is created |
| Fabricated run ID | Invocation blocked |
| Terminal turn run authority | Invocation blocked |
| Wrong session | Invocation blocked |
| Expired lease | Invocation blocked |
| Exhausted lease | Invocation blocked |
| Revoked lease | Invocation blocked |
| Authority source/ID mismatch | Invocation blocked |
| Grant action mismatch | Invocation blocked even with a valid lease |
| Resource path outside effective grant/sandbox policy | Invocation blocked |
| Symlink grant root or hidden path | Invocation blocked |
| Approved missing write root with create operation | Invocation allowed only under that grant root |
| Restart between approval and invocation | Invocation revalidates durable state before acting; current file-access coverage reopens the store at the tool boundary |
| Species does not expose tools | No parent/admin tool authority is available by resemblance |

## Current Test Anchors

- [`tool/execution_authority_continuity_test.go`](../../tool/execution_authority_continuity_test.go)
- [`tool/authority_access_test.go`](../../tool/authority_access_test.go)
- [`tool/native_file_tools_test.go`](../../tool/native_file_tools_test.go)
- [`runtime/work_executor_test.go`](../../runtime/work_executor_test.go)
- [`runtime/execution_authority_continuity_runtime_test.go`](../../runtime/execution_authority_continuity_runtime_test.go)
- [`durableagent/remote_child_test.go`](../../durableagent/remote_child_test.go)

The first anchor is the conformance matrix seed. The others cover concrete
regressions around fabricated run authority, native file grants, grant-backed
file operation audit rows, runtime propagation into native work execution,
durable group non-tool exposure, scheduled job scoping, and remote child
review-artifact protocol behavior.

## Remaining Integration Debt

The current implementation has a canonical durable run-authority record for
authority-sensitive tool and file-access invocation paths. Context no longer
serves as authority evidence by itself: callers must present a durable run ID,
and the tool boundary reloads and revalidates that run. Generic tool execution
does not mint run authority by searching for any compatible lease in the current
session. A lease becomes causal authority only at an explicit runtime admission
boundary, and one running turn cannot share the same lease binding with another
running turn. The run-authority row is immutable after admission; an exactly
identical write is idempotent, but changing principal, session, lease, species,
or the admission snapshot is rejected.

Capability-managed external tool evidence separates the authorization decision
from the execution outcome without splitting one logical invocation into two
unlinked rows. A successful point-of-use check records `status=allowed` and
`outcome_status=pending`; the executor finalizes that same invocation ID as
`completed` or `failed` through the original permit. Outcome finalization does
not reauthorize after the external effect has already run, so authority changes
during execution do not turn a successful side effect into an ambiguous failed
call. A turn whose authority binding fails during admission is terminalized as
failed instead of being left as a running turn.

Native file access is still pathname-based after authorization. The current
guard rejects symlink components in approved grant roots and revalidates
containment before use, but validation and filesystem operations are not one
descriptor-relative transaction. Child-controlled or otherwise adversarial
workspaces should not be treated as fully hardened until native file access uses
no-follow, beneath-root descriptor traversal for read, write, list, and search.

The matrix still distinguishes boundary-level conformance from real end-to-end
execution-species proof. A complete durable-child path test should cover
approved child request, durable lease, native work execution, internal turn, real
tool or file operation, persisted invocation evidence, store restart, and
revalidation or denial.

New execution species must either:

- create durable run authority and reuse the point-of-use validation path; or
- add an equivalent conformance row and tests before invoking
  capability-managed tools or native file resources.

This is not a reason to duplicate authority checks inside child-specific
adapters. The child substrate should remain vertically bounded; the horizontal
bridge belongs at the execution-authority boundary.
