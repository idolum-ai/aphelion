# Organic Agent-Owned Tools Proposal

_Status: non-normative research note._

Current shipped target: external tool manifests may be installed, audited,
probed, verified, registered, granted, invoked, marked stale, rolled back, and
uninstalled when they use supported `process`/`subprocess` execution. Not
current target: a generic agent-owned tool platform, plugin marketplace,
container execution, workspace-runner execution, or domain-tool ecosystem.
Aphelion stops short because tools should stay boring, granted, sandboxed, and
operator-legible until a concrete governed workflow requires more.

This document captures research context after the current `tool_authority`
rollout.

The immediate lesson from the current branch is that the lifecycle work is
useful, but the implementation is still too core-centric. Aphelion now has a
real authority chain for tools:

1. capability request
2. parent/admin review
3. install
4. audit
5. probe and verify
6. registration
7. capability grant
8. invocation-time authorization

That chain is worth keeping.

The research question is **where tool implementation should live**.

The goal is not to turn Aphelion into a growing library of domain tools.
The possible longer-term goal is to let approved agents request, provision,
attest, register, grant, and use **their own tools** while Aphelion remains the
governance, diagnostics, and continuity substrate. That is not the current
done-done release target.

## Directional Thesis

Aphelion should behave less like a central SDK with ever more built-in
capabilities and more like an organic system with:

- lightweight contracts that make intent tangible
- explicit authority records
- installation and probe evidence
- drift diagnostics when declaration and behavior diverge
- repair-oriented status rather than fortress-style locking

The system should preserve security, but security should come primarily from:

- bounded execution environments
- explicit authority and grant state
- attested install/probe records
- invocation-time authorization
- diagnosable drift

not from endlessly embedding special-purpose tools in core.

## Architectural Reframe

### Keep in Aphelion core

Aphelion should continue to own:

- capability request and review flow
- registration records
- capability grants
- invocation-time authorization checks
- canonical execution logging
- status and drift projection
- operator-readable diagnostics

### Move out of Aphelion core

If this research direction is pursued, domain implementation should move to
agent-owned tools instead of expanding core.

Examples:

- browser automation for the external child agent
- camera/monitoring tools for a home agent
- channel-specific extractors
- small environment-specific watchers

These should not require a new specialized core feature every time, but they
also should not become a live marketplace or generic plugin substrate.

## Current Pressure Points

### 1. Core-embedded domain tools

Built-in domain tools are architecturally expensive. If future domain
capabilities follow that pattern, Aphelion will become a collection of bespoke
tools instead of a general authority layer.

### 2. Registration is still runtime-definition-centric

`tool/tool_authority.go` currently assumes trusted runtime tool definitions that
live inside the repo. The research direction is to allow registration of
approved external tool packages, containers, or manifests with attestation
metadata. The current release target does not include container or
workspace-runner execution.

### 3. Dependency installation is not yet first-class

Right now dependency installation is mostly framed as an operational request.
That is necessary, but incomplete. The system needs a durable record of:

- what was requested
- what was installed
- where it was installed
- whether the post-install probe passed
- what rollback path exists

### 4. Status is lifecycle-aware, but not fully drift-aware

Status can now surface recent request, grant, registration, audit, and probe
transitions.
The next step is to compare declared state against executable reality.

Examples of desired diagnostic states:

- requested-but-never-approved
- approved-but-not-installed
- installed-but-not-registered
- registered-but-not-granted
- granted-but-probe-failing
- contract-diverges-from-behavior

## Research External Tool Model

The core abstraction should be a **registered external tool manifest**, not a
new built-in tool for every domain.

Minimal fields:

- `tool_name`
- `owner_agent`
- `execution_mode` (`process`, `container`, `workspace_runner`, later others)
- `entry_ref`
- `contract`
- `install_requirements`
- `probe`
- `rollback`
- `attestation`

### Example manifest shape (draft)

```json
{
  "tool_name": "browse_page",
  "owner_agent": "child-alpha",
  "execution_mode": "container",
  "entry_ref": "ghcr.io/idolum/child-browser-tool:pilot",
  "contract": {
    "inputs": {
      "url": "string",
      "goal": "string"
    },
    "outputs": {
      "title": "string",
      "final_url": "string",
      "summary": "string",
      "links": [{"label": "string", "url": "string"}]
    },
    "constraints": [
      "read_only",
      "no_login",
      "no_form_submission",
      "same_task_navigation_budget<=3",
      "timeout<=30s"
    ]
  },
  "install_requirements": {
    "packages": ["playwright", "chromium"],
    "host_requirements": ["container_runtime"],
    "notes": "Playwright + Chromium live with the tool image, not in Aphelion core."
  },
  "probe": {
    "kind": "command",
    "command": ["/tool/probe", "--self-check"]
  },
  "rollback": {
    "kind": "container_remove",
    "notes": "Remove image and revoke grants if probe fails repeatedly."
  },
  "attestation": {
    "installer": "aphelion",
    "installed_at": "2026-04-23T00:00:00Z",
    "probe_status": "passed"
  }
}
```

The purpose of this manifest is not to simulate an SDK. It is to make
capability, intent, and recovery tangible.

## Governed Dependency-Install Lane

To let agents propose installing dependencies for their own capabilities, the
system needs a separate governed lifecycle for installation.

This should not be implicit inside tool registration.

### Install lifecycle

1. **capability need identified**
2. **install request submitted**
3. **approved / denied / overridden**
4. **install executed in bounded environment**
5. **post-install probe run**
6. **attestation recorded**
7. **tool registration allowed**
8. **grant allowed**

### Install Request Fields

- `requested_by`
- `target_environment`
- `dependencies`
- `why_now`
- `bounded_effect`
- `probe_plan`
- `rollback_plan`
- `status`

### Traceability annotations

Decision and lifecycle records should carry a compact traceability shape:

- `rationale`: short human or machine explanation for why this transition happened
- `artifact_refs`: typed references with `{kind, ref, label?}`

This should be attached at least to:

- pending/install decisions
- install records
- probe records
- audit records

Reference kinds should stay open-ended but start with durable things we already have,
for example:

- `file_path`
- `git_commit`
- `artifact_id`
- `review_event`
- `execution_event`
- `telegram_message`
- `session_message`

Message references should only be stored when there is a durable identifier already
available in canonical state. If the system only has ephemeral conversational context
and no durable message/store handle, it should fall back to inline `rationale` text
instead of pretending the message is traceable.

This keeps decision provenance legible without forcing every stage record to become a
full commentary blob.

### Why separate install from registration?

Because these are different truths:

- install answers: *is the environment provisioned?*
- registration answers: *is this tool recognized by authority state?*
- grant answers: *who may use it?*
- invocation answers: *does this call pass current authorization and runtime checks?*

Keeping those separate makes drift repairable.

## Organic Diagnostics

The more organic model is not weaker. It shifts emphasis.

Instead of building thicker walls inside core, Aphelion should become better at:

- identifying declaration/behavior mismatches
- surfacing degraded capability states early
- proposing repairs
- preserving operator legibility

Examples of status output that this architecture should enable:

- "external child agent proposed `browse_page`; install approved; probe failing because Chromium shared libs are missing; tool remains unregistered"
- "camera watcher tool is installed and registered, but its grant was revoked after repeated probe failures"
- "tool contract declares read-only behavior, but observed outputs indicate mutation attempts; quarantine recommended"

This is closer to an immune-system model than a padlock model.

## Current Implementation Status

The first external-tool lane is implemented for process-style tools:

- traceability (`rationale` plus typed `artifact_refs`) flows through pending
  decisions, lifecycle records, runtime-authored install/audit/probe outcomes,
  and the `/status` projection
- verification is strict: `install_set status=verified` requires current
  runtime-authored `audit_run` and `probe_run` records
- audit/install/probe records persist deterministic anchors for `install_ref`,
  normalized manifest hash, and process workspace or container identity
  fingerprints
- `install_show`, `audit_show`, registration, grants, principal manifest
  listing, and invocation re-check the current anchors and mark drifted tools
  stale with typed operator-readable reasons such as `manifest_drift`,
  `workspace_drift`, `container_drift`, and `install_ref_changed`
- stale external tools cannot be registered or invoked
- process/subprocess invocation, install commands, and probe commands run
  through the same sandbox runner boundary as normal tool execution
- process-mode network/filesystem/duration ceilings are enforced at runtime for
  install, audit checks, probe, and invocation
- container manifests have separate image/build/health audit and drift
  semantics, but remain non-executable until a dedicated container executor
  exists
- tenants and agents create requests through `capability_request`; operators
  retain authority over parent/admin review, grants, install, audit, verify,
  and register
- `external-tools/browse_page/` is the first pilot manifest, owned by
  `child-alpha`, with a deterministic fixture implementation outside core

## Research Migration Sketch

This sketch is retained for future evaluation. The current done-done target has
already stopped at the narrow verified `process`/`subprocess` path.

### Phase 1 — stabilize current authority substrate

Keep the current `tool_authority` lifecycle and status projections.
Keep domain behavior outside core unless it is genuinely native system
infrastructure.

### Phase 2 — research external-tool manifest registration

Extend registration so a tool may be registered from an attested external
manifest / package / container reference.

Core should validate:

- manifest shape
- owner agent identity
- attestation presence
- probe status
- authority-managed registration path

### Phase 3 — add governed install + attestation records

Introduce durable install records distinct from capability requests and registered
tools.

Minimum records:

- install request
- install execution event(s)
- probe result
- attestation record
- rollback / repair recommendation

### Phase 4 — research generic external tool executor

If a concrete governed workflow justifies it, add one generic execution path in
Aphelion for approved external tools beyond the current process/subprocess path.

It should be:

- execution-mode aware
- contract-aware
- authority-aware
- diagnostics-aware

It should **not** be browser-aware, camera-aware, or domain-aware.

### Phase 5 — first external-tool pilot

Use the external child agent as the first pilot:

- propose `browse_page`
- approve install requirements
- provision dependencies with attestation
- register external manifest
- grant invocation only to the external child agent
- validate one narrow workflow

If this pilot requires special-casing browser behavior inside Aphelion core,
then the abstraction is wrong and should be revised before expanding further.

## What This Gives Us

If implemented, the system becomes:

- more dynamic: agents can grow tools without waiting for core specialization
- more expressive: capabilities live where they belong, near the agent using them
- more secure: authority, install, grants, and invocation remain explicit
- more diagnosable: state drift becomes legible
- more repairable: failure states can lead to bounded recovery steps

## Immediate Recommendation

The next concrete artifact should be a one-page **external-tool manifest spec**
and a companion **governed install/attestation record spec**.

Not Playwright-in-core.
Not another built-in domain tool.

Those specs are the shortest path from the current branch to the agent-owned
future.
