# Execution Friction Inventory

Status: draft operations inventory, not canonical architecture.

This note records issues observed during a recent live source-install cycle and
follow-on durable-child repair attempt. The service remained up, but operator
experience and durable-child execution exposed several safety and workflow
gaps. The goal is to preserve the problem classes as reviewable release debt
and to track the first implementation slice that turns the map into runtime
state.

This document belongs under operations because it is an incident-shaped
hypothesis map. The canonical contracts remain in architecture and requirements
documents; this inventory points back to those contracts where possible.

The executable traceability map lives in
[`execution-friction-test-surface.json`](execution-friction-test-surface.json).
Each observed problem has one manifest row with existing contracts,
always-on test anchors, opt-in scenario-spec anchors, current status, and the
ideal invariant. Always-on anchors are traceability to nearby implemented contracts;
they are not a claim that the incident has a direct regression test unless the
linked test says so. Opt-in executable debt specs can be run with
`APHELION_RUN_FRICTION_EVALS=1 go test ./... -run ExecutionFrictionDebtSpec` to
audit the full surface. The current slice exercises production seams for
audience-aware tool-result projection, durable workflow next-state records,
uncertain-effect verification state, resource preflight classification, typed
repair-operation registration, durable-child wake/task identifiers, source-status
classification, and persistence-latency classification. The restart-spanning
child/native-work-to-tool flow and the complete compact child task protocol are
still acceptance-level specifications rather than full end-to-end runtime
reproductions. A failure means an ideal next-state, exposure, or authority
outcome is still missing.

## Evidence Window

- Evidence sources: user-service journal, turn-run records, execution events,
  capability invocation records, effect-attempt records, and read-only schema
  checks.
- The database schema was current. A separate state-consistency repair was
  needed for missing derived session rows.
- This document intentionally omits concrete local paths, agent names, record
  identifiers, chat identifiers, and credential-adjacent filenames because the
  repository is public.

## Observed Problems

1. A command classified as read-only produced model-visible output containing
   sensitive-looking material without an audience-appropriate exposure
   projection.
2. Tool output sensitivity was not independently gated from command authority;
   a read-only command could still expose sensitive output.
3. Some persisted `result_preview` fields contained sensitive-looking words and
   had no redaction markers.
4. Sensitive path and config metadata appeared in ordinary tool previews during
   child repair work.
5. The approval flow did not reliably turn an approved grant or continuation
   into an obvious next executable step.
6. Capability grants could wake a child while the next concrete lease/action
   remained unclear to the operator.
7. Several continuation callbacks ended as `outcome_unverified` and blocked
   retry instead of producing a bounded verification surface.
8. Work executor failures reported "side effects require verification before
   retry" without an ergonomic follow-up path.
9. Raw exec rejected path-qualified executables that were natural for child
   runtime repair.
10. Raw exec rejected interpreter and sublanguage use such as Python and sed
    during child-local patching.
11. Multi-effect command splitting was correct for safety but created repeated
    low-progress turns.
12. Continuation envelopes rejected effects that were plausible next repair
    steps but were not represented in the approved envelope.
13. `update_operation` could not rewrite an in-progress executable phase while
    its lease was active, and the operator-visible escape hatch was not smooth.
14. Durable-child file reads were initially outside native sandbox read roots.
15. Durable-child file writes were initially outside native sandbox write roots.
16. After grants made paths reachable, POSIX permissions still blocked the
    intended child-local config write.
17. A capability-managed external tool invocation failed once because durable
    run authority evidence was missing.
18. Durable-child communication took too many wake, poll, acknowledgement, and
    report turns for a small concrete repair.
19. Parent/child protocol output was not compact enough to converge quickly on
    a child-local configuration materialization task.
20. Token budget exhaustion occurred repeatedly during the repair loop, likely
    increasing circular recovery behavior.
21. Cached release metadata made a current source build appear degraded because
    a tagged release string looked newer.
22. Slow transparent-execution-sequence writes appeared repeatedly during
    background recommendations and mission assessment.
23. Transient provider and Telegram transport failures occurred, but were not
    the main cause of the repair loop.
24. The child substrate has strong vertical concepts, but the horizontal bridge
    across approval, lease, sandbox roots, host permissions, child wake, and
    concrete tool execution remains too cumbersome.

## Working Interpretation

The failures are not one local regression. They cluster around causal closure:
after approval, execution, blocking, uncertainty, resource failure, or child
reporting, the system did not always compile the stop into a durable,
operator-legible next state.

This is not the same as saying Aphelion lacks an execution-authority bridge. The
bridge exists: [`execution-authority-continuity.md`](../architecture/execution-authority-continuity.md)
defines the horizontal contract between authorized work and concrete tools or
resources. The incident indicates that some execution species, recovery paths,
and operator projections did not yet conform to or complete the workflow around
that spine.

The better model is not one larger atomic transaction. The chain crosses SQLite,
the runtime, durable children, the kernel, tool processes, and sometimes
external services. It should be a durable saga with causal closure:

```text
operator approval
  -> continuation or capability grant
  -> durable child wake
  -> child-local filesystem authority
  -> concrete tool execution
  -> output sensitivity
  -> verification or next bounded step
```

Each boundary keeps its own current point-of-use checks. This PR introduces the
first durable next-action ledger for that handoff: `next_action_records` and a
matching `workflow.next_state` execution event. The intended property is that
every nonterminal stop should deterministically create one typed next durable
state. The incident examples include `ready_to_execute`,
`blocked_needs_authority`, `blocked_needs_resource_repair`,
`needs_verification`, `waiting_for_child`, `waiting_for_operator`,
`scheduled_retry`, `external_dependency`, `superseded`, `cancelled`, and
`terminal`; this list is a starting vocabulary, not a repository-wide state
machine.

That next-state record should carry causal IDs, owner, exact next operation,
required authority, retry semantics, verifier where applicable, and the operator
projection that should be rendered.

## Planes

The observations separate into four interacting planes:

- **Authority plane:** comparatively mature. Execution-authority continuity,
  continuation leases, grants, and point-of-use checks are implemented contracts.
- **Workflow plane:** now has a first-slice next-action ledger for approvals,
  uncertain effects, supersession, child wakes, and resource blockers. Full
  execution-species conformance remains active debt.
- **Presentation plane:** incomplete or inconsistent projection of the exact next
  action when the system stops safely; compact tool-result projections and
  durable-child task/result identifiers are the first slice.
- **Exposure plane:** now projects ordinary turn-run tool previews through an
  audience-aware policy. Other output, evidence, log, and privileged hydration
  routes still need the same projection model.

## Diagnostic Matrix

| Observation | Evidence class | Existing contract | Probable owner | Confidence | Reproduction shape | Expected correction |
|---|---|---|---|---|---|---|
| 1-3. Read-only output carried sensitive-looking material into previews | Tool result preview and execution event metadata | Command effect planning; evidence redaction; visibility rules | `tool`, `runtime`, `session` | High | Read-only command returns credential-adjacent content | Audience-aware output projection before prompt/log/hydration exposure |
| 4. Sensitive path/config metadata appeared in previews | Tool previews and errors | Config visibility and durable-child forensic redaction policies | `tool`, `runtime` | Medium | Inspection or error mentions secret-adjacent paths | Path/config sensitivity class, not only secret-shaped redaction |
| 5-6. Approval/grant did not always become next executable work | Continuation and capability events | Action proposal, continuation leases, execution-authority continuity | `runtime`, `session`, `tool` | High | Approve grant/continuation then hit hidden downstream blocker | Post-decision next-state record with exact executable, authority need, or blocker |
| 7-8. Outcome uncertainty blocked retry without smooth follow-up | Effect-attempt and continuation-blocked records | Effect-attempt ledger | `runtime`, `session` | High | Side effect fails ambiguously | Auto-materialize bounded verifier or terminal escalation |
| 9-12. Shell hardening rejected plausible child repair commands | Exec failures and effect-attempt rejections | Bounded action; raw shell is transport, not authority model | `commandeffect`, `effectauth`, `tool`, `runtime` | High | Path-qualified, interpreter, dynamic, or multi-effect commands | Typed child-local operations or confinement, not wider raw shell |
| 13. Active leased phase could not be rewritten smoothly | Operation update failure | Continuation lease immutability and supersession | `runtime`, `session` | High | Try to rewrite active executable phase | First-class retire/supersede-and-offer replacement path |
| 14-16. Child-local reads/writes hit root and OS permission mismatches | File tool failures and host mode checks | Execution-authority continuity; native file authority | `tool`, `runtime`, `durableagent` | High | Granted child-local resource is outside roots or unwritable | Resource preflight combining grant, sandbox, path identity, and host mode |
| 17. Capability-managed tool lacked durable run authority evidence | Capability invocation record | Execution-authority continuity | `tool`, `runtime` | High | Tool invocation with grant but missing causal run permit | Species conformance test and run-authority propagation at entry point |
| 18-19. Child communication took many turns | Durable-child wake and parent turn sequence | Durable child product contract | `durableagent`, `runtime` | Medium | Small child-local materialization repair | Compact typed child task/result protocol |
| 20. Token budget exhaustion during repair loop | Turn usage records | Memory/context budget docs | `runtime`, `pipeline` | High | Long approval/repair/recovery loop | Compact current-state packets and pull-oriented hydration |
| 21. Source install looked degraded under release metadata | Status output | Deploy/status contracts | `internal/standalonecli`, `runtime` | High | Source build with stale/latest release metadata | Mode-aware status separating source revision match from release freshness |
| 22. Slow TES writes under event volume | Journal warnings | Transparent execution sequence | `session`, `runtime` | Medium | High event-volume repair turn | Persistence performance or batched background projection review |
| 23. Provider/transport transients | Journal warnings | Reliability requirements | Provider/transport integration | Medium | Stream or Telegram transient | Typed external incident classification; not root-cause bucket |
| 24. Child substrate strong vertically but cumbersome horizontally | Full sequence across parent, child, grants, tools | Durable child and execution-authority docs | `durableagent`, `runtime`, `tool` | High | Child-local repair that crosses approval, wake, file, and tool layers | End-to-end species conformance plus compact child task protocol |

Items 21-23 are amplifiers or adjacent status/reliability issues, not the same
root cause as the authority/workflow/exposure loop.

## Exposure Model Correction

"Universal exposure gate" should not mean one final regex scanner. Model
context, operator UI, logs, canonical evidence, external delivery, and
diagnostic traces are different audiences with different retention and
disclosure needs.

The desired shape is:

- protected canonical evidence;
- typed sensitivity and provenance;
- explicit audience and purpose;
- audience-specific projections;
- redacted view, digest, withheld marker, or protected artifact reference;
- auditable privileged hydration when justified.

## Implementation Slice And Remaining Work

- Tool-result previews now pass through `ProjectToolResultForAudience` before
  they are stored on turn runs. The first projection covers model-preview
  exposure; canonical evidence hydration, logs, operator UI, and protected
  artifact access still need the same explicit audience model.
- Approvals, uncertain effects, resource preflight failures, child wake
  materialization, and supersession can now record typed next-action rows. The
  remaining work is to ensure every execution species emits those rows through
  its real transition path instead of relying on nearby boundary tests.
- Raw shell remains deliberately hard to widen. The new typed repair-operation
  registry gives rejected child-repair shapes named alternatives, but execution
  support for those alternatives is still future work.
- Durable wake events now carry task-packet and result identifiers. The compact
  child protocol is not complete until a real child-local repair can converge
  through one task packet, one typed result or blocker, and a restart-spanning
  native-work-to-tool flow.
- Source status now distinguishes a verified source checkout from stale release
  metadata. Persistence latency is classified as an operational amplifier, not
  as the causal root of the authority/workflow loop.
