# Operations — Session-Native Operational Work, Proposals, and Material Gates

## Overview

Aphelion should handle open-ended work more like a competent firm or investigator than a turn-bounded tool loop.

The operating principle is:

- autonomous work inside an authorized domain
- explicit gates when a hypothesis becomes material in action or effect
- one public-facing principal
- durable session state rather than ad hoc per-tool memory

This means the system should not flatten everything into "dangerous command approval."

Instead, it should distinguish:

- **reasoning and assessment**: free to proceed inside the case
- **proposal gates**: explicit user approval when the next move materially expands capability, risk, cost, privacy scope, or external effect
- **escalation**: a higher-authority path when the current principal cannot authorize the move

The result is a session-native operational protocol:

- `operation`: the durable working state for the current objective
- `proposal`: the currently pending or most recent gate package inside that operation
- `finding`: a bounded claim with confidence and basis
- `artifact`: a produced or inspected file/media/reference associated with the work

## Scope

This spec does not require a separate case-management subsystem.

The operation record should live inside the existing session ledger as sidecar durable state, the same way plan state already does.

That choice matters:

- the session already has identity, persistence, prompt assembly, and replay semantics
- operations should survive retries, long turns, compaction, and transport interruptions
- the visible transcript remains the user-facing conversation
- operation state is machine-owned sidecar state, not a hidden replacement transcript

## Truth-Class Contract (Normative)

Operation protocol fields participate in the shared four-class surface taxonomy:

- `operation` and embedded `proposal` are `operational current-state store`
  surfaces for mutable declared work state.
- execution sequencing and tool/delivery evidence remains `canonical` in TES
  (`session.execution_events`), not in operation sidecars.
- user-visible rendering of operation state (`/status`, `/health trace`, quick-read) is
  a `projection`.
- removed surfaces must be deleted or rejected instead of being consulted by
  operator projections.

Operational implications:

- operation sidecars can describe intent, stage, and pending gates;
- operation sidecars cannot silently rewrite canonical execution history;
- projections must source-attribute canonical and operational data and must not
  invent execution history.
- operation completion evidence should expose typed reason codes for missing
  evidence, so status/doctor projections do not depend on matching exact prose.

## Core Rule

Aphelion should assume that both humans and model actors are intelligent and autonomous.

Autonomy should therefore be the default **between gates**, not across them.

The boundary is not "before every tool call."

The boundary is "before materially consequential expansion or effect."

## Operation Protocol

Each session may have zero or one active operation record.

The minimal protocol is:

### Operation

- `id`: stable operation id
- `objective`: what the system is trying to accomplish
- `status`: `idle`, `active`, `blocked`, `completed`, or `failed`
- `stage`: current operational phase
- `summary`: short current-state summary
- `proposal`: embedded current or most recent proposal state
- `findings`: bounded claims accumulated so far
- `artifacts`: references to relevant produced or inspected artifacts
- `updated_at`: durable freshness marker

### Proposal

- `id`: stable proposal id
- `kind`: high-level gate type
- `summary`: what is being proposed
- `why_now`: why the gate has been reached now
- `bounded_effect`: what will happen if approved
- `status`: `pending`, `approved`, `denied`, `expired`, or `superseded`
- `updated_at`: durable freshness marker

### Finding

- `claim`: bounded claim
- `confidence`: `low`, `medium`, or `high`
- `basis`: short provenance or basis statement

### Artifact

- `label`: human-readable name
- `ref`: path, external id, or other stable reference

The protocol is intentionally minimal. It should be general enough for:

- repository work
- operational tasks
- investigation
- capability acquisition
- staged external actions

## Stages

Stages are not a strict workflow engine. They are a durable sketch of where the operation is.

Useful default stages are:

- `intake`
- `assessment`
- `reconnaissance`
- `hypothesis`
- `proposal`
- `execution`
- `synthesis`
- `delivery`

Implementations may use a smaller subset at first, but the stored stage should still be explicit.

## Material Gates

The gate model should be proposal-shaped rather than command-shaped.

The first-class question is not:

- "is this shell command dangerous?"

The first-class question is:

- "has the work reached a threshold where explicit approval is now required?"

Typical proposal-triggering thresholds include:

- acquiring a new capability for the operation
- installing dependencies or tools
- enabling or using networked/external access
- contacting a third party
- entering a privacy-sensitive or surveillance-like phase
- performing destructive or irreversible mutation
- materially increasing time, cost, or compute commitment
- publishing a consequential conclusion with limited confidence

## Proposal Semantics

Proposals should describe a bounded package of work, not a raw command.

Good proposal shape:

- what I can do
- why approval is needed
- the bounded effect if approved
- the expected output or next deliverable

Bad proposal shape:

- raw shell syntax with no operational framing
- vague "approve?" requests detached from a real threshold

Example:

- "I do not currently have browser automation in this operation. I can acquire it locally by installing Playwright, using network access to browse `reddit.com`, and returning a screenshot. Approving this proposal will install the dependency in the workspace, visit one site, and generate one image artifact."

## Relationship To Plans

Plans and operations are related but not identical.

- `plan` is a concise execution checklist
- `operation` is the broader durable operational state

The plan may be projected from the operation, but it should not be the only source of truth for operational work.

This distinction matters because many real tasks require:

- stage changes
- pending proposals
- findings
- produced artifacts

Those do not fit cleanly into a single checklist.

## Relationship To Brokerage

Bounded Idolum/Aphelion brokerage remains useful, but it should no longer be the only place where initiative or planning shape appears.

Brokerage may:

- shape the next turn
- seed or revise operation state
- pressure for a proposal or a question

But operations themselves may continue across turns until:

- a proposal gate is reached
- a user question is required
- delivery is ready
- policy or budget forces a stop

## Relationship To Continuation Leases

Continuation leases are the operational mechanism for carrying approved work
across turn boundaries. An approved multi-turn lease may continue automatically
inside its remaining turn budget when the next action stays inside the same
bounded authority.

This is autonomy between gates, not autonomy across gates. The runtime must stop
and require a fresh operator boundary when:

- the lease is exhausted, expired, revoked, or stale against the operation plan;
- the next step asks for a new lease class, new allowed action, new required
  capability grant, deploy/restart authority, or external effect not covered by
  the lease;
- Mission Ledger state says the mission is completed, blocked, archived,
  expired, or dormant;
- the approved bundle no longer matches the current phase-plan fingerprints.

Runtime may reduce approval friction by consuming a newly materialized phase or
proposal under an already-active lease, but only when the lease class,
allowed-action set, and required grants are structurally covered by the active
lease. This reuse is a transport/runtime optimization, not a new authority
source and not a lease renewal.

## Relationship To The Decision Broker

The decision broker should remain the transport/runtime mechanism for waiting on explicit user choices.

Its job is not to define proposal semantics.

Instead:

- operation state defines the proposal
- the decision broker transports the pending approval
- Telegram, CLI, or other channels render the choice

This keeps proposal semantics durable and session-native while keeping the interaction transport-neutral.

The same pattern should be reused for child setup work.

Creating a durable external-channel child is an operation:

- objective: set up a bounded external-channel child
- stage: proposal, configuration, connection, activation
- proposal: adapter choice, capability acquisition, credential binding, or activation threshold

That lets the setup behave like guided operational work instead of a one-shot static form.

## Relationship To Tool Authority Lifecycle

Operation proposals and tool capability requests are related but distinct.

- operation proposals gate bounded work inside an operation (`pending/approved/denied/...`)
- tool capability requests gate capability rollout (`proposed/parent_approved/approved/rejected`, then install/audit/verify/register/grant)

Normative boundary:

- operational proposal state must not be treated as proof that a tool is registered or granted.
- tool invocation authority must come from `capability_grants`, verified tool lifecycle state, and current invocation-time checks.
- status should keep these layers separate so "request approved" is not conflated with "tool registered/granted."

## Persona Rule

The public-facing persona remains `Idolum`.

Internal actors may inspect, debate, or execute, but the public surface should present:

- current limits
- current stage when useful
- the next proposal when a gate is reached
- the final result

The user should not see:

- governor handoff language
- hidden subsystem narration
- accidental contradiction where Idolum says "I can't" while the system continues anyway

If capability is missing but acquirable, the visible response should generally be:

- "I do not currently have X in this operation, but I can acquire it if you approve this proposal."

## Prompt Requirements

The governor prompt should receive:

- current operation state
- current proposal state
- discipline that autonomy is expected between gates
- discipline that proposals are required at material thresholds

The face prompt does not need the full operation object, but it should remain honest about:

- visible limits
- pending approval state
- final delivery state

## Tooling Requirements

The governor should be able to update the operation state explicitly with a dedicated tool.

That tool should be capable of:

- inspecting the current operation state
- replacing or merging operation fields
- updating the embedded proposal
- adding findings and artifacts

The runtime may also update operation state directly when real transport/runtime events occur, such as a proposal being approved or denied.

## Decisions

- **Operations are session-native.** They belong in the existing session store.
- **Autonomy lives between gates.** The system should not ask permission for every minor step.
- **Continuation loops stay inside gates.** Approved leases may consume multiple
  turns, but they must not renew, widen, or cross material thresholds silently.
- **Proposals gate material thresholds.** They are broader than dangerous-command confirmations.
- **Plans remain useful but insufficient.** Operational state needs more than a checklist.
- **One public persona.** Internal autonomy must not fracture the visible relationship surface.
