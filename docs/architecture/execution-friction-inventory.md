# Execution Friction Inventory

Status: draft incident inventory.

This note records issues observed during a recent live source-install cycle and
follow-on durable-child repair attempt. The service remained up, but operator
experience and durable-child execution exposed several safety and workflow
gaps. The goal is to preserve the problem classes as reviewable release debt
before choosing fixes.

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
   sensitive-looking material without requesting approval first.
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

The failures are not one local regression. They cluster around a broader
integration gap:

```text
operator approval
  -> continuation or capability grant
  -> durable child wake
  -> child-local filesystem authority
  -> concrete tool execution
  -> output sensitivity
  -> verification or next bounded step
```

Each boundary has local safety checks, but the combined path is not yet a smooth
transaction. The system is often safe by stopping, but poor at turning a stop
into the next crisp, reviewable action.

## Questions For Follow-Up

- Should output sensitivity require a prompt-exposure gate independent of
  command effect classification?
- Should approval consumption atomically enqueue the next bounded executable or
  verification surface?
- Should durable children have a compact "perform one local materialization
  task and report typed result" protocol?
- Should child-local configuration slots be modeled as typed resources that reconcile
  Aphelion grants, sandbox roots, and host filesystem permissions?
- Should source installs suppress stale release-metadata degradation when the
  running binary matches the checkout?
