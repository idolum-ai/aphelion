# Durable Agents — External Sensory Organs, Quarantine, and Parent Governance

## Overview

Aphelion needs a first-class model for **durable external-channel agents**.

These agents are not ordinary task subagents.
They are persistent subordinate organs attached to the house through an external ingress surface such as:

- email
- Telegram groups
- website chat
- remote host agents
- other future public or semi-public channels

A durable agent may have its own runtime, storage, and local channel continuity.
It may even live on another machine.
But it is still constitutionally subordinate to the parent house.

The core problem is simple:

external interaction can steer a model.

So the architecture must prevent outside users, group chats, inboxes, public websites, or remote-machine agents from writing directly into the core system's identity, prompt surface, durable memory, or authority model.

Durable agents exist to solve that problem without giving up rich external sensing or long-lived channel presence.

## Telos

Durable agents should let Aphelion:

- attach to external channels safely
- preserve continuity within those channels
- execute bounded local actions
- synthesize important state upward to the parent
- remain steerable by the admin without becoming steerable by the public

They are sensory organs with local processing, not new sovereign selves.

## Scope

### v0 required

- a durable-agent concept distinct from ordinary subagents
- parent/child governance model for durable agents
- default quarantine boundary between child-local state and parent state
- bounded upward synthesis as review artifacts
- admin-ratified drift model for durable agent prompt/memory/behavior changes
- one parent heartbeat for the house; durable agents wake from source events or polling by default
- dormancy model for durable agents when idle
- isolation rules for local and remote durable-agent runtimes

### Deferred after v0

- full runtime implementation for remote durable-agent registration
- public website durable-agent deployment flow
- bidirectional policy sync protocol between parent and remote durable agents
- durable-agent marketplace / fleet management
- richer operator UX for agent charter editing, review, and attestation

## Core Principles

1. **Durable agents are quarantined subordinate organs.**
   They are persistent and long-lived, but they do not become independent constitutional centers.

2. **External ingress must not write directly into the house.**
   No durable child may durably modify parent prompts, memory, or authority through outside interaction alone.

3. **Upward flow is artifact-shaped.**
   The child reports upward through bounded review artifacts, not raw transcript injection by default.

4. **Durable drift is admin-ratified.**
   Long-lived prompt, memory, charter, or policy changes to the durable agent must be approved in the admin conversation and recorded as provenance.

5. **Trust changes local latitude, not constitutional ownership.**
   A family agent may have a wider local charter than a public website agent, but neither owns the center.

6. **One heartbeat for the house.**
   The parent heartbeat belongs to Aphelion. Durable agents do not own independent heartbeats by default; they wake from source events or bounded polling.

## Durable Agent vs Ordinary Subagent

Ordinary subagents are delegated work sessions.
Durable agents are ongoing external-channel presences.

Ordinary subagents:

- are usually task-bounded
- often short-lived
- primarily serve internal delegation
- return completion artifacts

Durable agents:

- maintain a standing external attachment
- may receive untrusted or semi-trusted inbound events over time
- may hold local channel continuity
- must defend the parent house from external drift
- synthesize recurrently upward for review and ratification

The systems may share runtime infrastructure, but the constitutional model is different enough that durable agents need their own spec surface.

## Durable Agent Model

Each durable agent should have:

- `agent_id`
- `parent_agent_id` or parent house identity
- `channel_kind` (`email`, `telegram_group`, `web_chat`, `remote_host`, etc.)
- `charter`
- `capability_envelope`
- `local_storage_roots`
- `network_policy`
- `wakeup_mode`
- `drift_policy`
- `status`
- `created_at`
- `updated_at`

### Charter

The charter defines what the durable agent is for.

Examples:

- digest inbound email and escalate important items
- act as a bounded family helper inside a Telegram group
- observe laptop resource pressure and take pre-approved remediations
- act as a public website greeter with no power to mutate the parent house

The charter is derivative of the parent house identity, but narrower and task-focused.

## Constitutional Boundary

The constitutional rule is strict:

**No durable child writes directly into parent prompt or memory space.**

The only normal upward path is a bounded review artifact.

That means external interaction alone must not:

- rewrite parent prompt files
- rewrite child prompt files durably
- mutate curated parent memory
- grant new tools or authority
- promote imported files into ordinary parent retrieval
- change the child's charter or allowed actions permanently

Those changes may happen only through parent review and admin ratification.

## Local Accommodation vs Durable Drift

Durable agents may accommodate local conversation within their current envelope.

Examples:

- a family group agent may answer in a warmer tone inside the group
- an email agent may summarize a PDF for the current review cycle
- a website greeter may adapt to the visitor's question within its public sales/support charter

But local accommodation is not durable drift.

Durable drift includes:

- changing summary shape or escalation policy long-term
- adopting a new standing role (for example, "you are now my software developer")
- enabling outbound replies where they were previously forbidden
- installing new packages or gaining new host privileges
- changing prompt or memory files that affect future behavior

Durable drift must be negotiated with the admin in the parent conversation and recorded as provenance.

## Upward Synthesis and Review Artifacts

Durable agents should synthesize upward into bounded review artifacts.

A review artifact may include:

- source channel / durable-agent identity
- interval covered
- concise digest of what happened
- actions taken locally
- files or links surfaced
- attempted drift candidates
- questions requiring admin decision
- risk or anomaly flags
- optional child-session or run reference

The parent house reviews that artifact first.
The parent may then send an admin-facing synthesis for final ratification before broader retention or reframing.

### Raw transcript discipline

Full durable-agent transcripts should remain inspectable sidecar state, not ordinary parent prompt input.

The parent may inspect them explicitly when needed.
They should not be the default upward path.

## Memory Zones

The system should treat durable-agent state as three zones.

### 1. Child-local working memory

Used by the durable agent to do its job.
This may include:

- recent channel context
- local summaries
- pending actions
- source-specific metadata

This state is not automatically trusted by the parent.

### 2. Parent review artifact

The synthesized upward artifact the parent sees first.
This is the normal review surface between child and parent.

### 3. Admin-ratified durable memory

Only after parent review and admin ratification should information be promoted into broader parent memory, policy, or retrieval surfaces.

This keeps the external world from writing directly into the center of the house.

## Wakeup and Heartbeat Model

The default shape is:

- **one heartbeat for the house**
- **event- or poll-driven wakeups for durable agents**

Durable agents should wake through source-appropriate mechanisms:

- inbound email poll or push notification
- group message / mention event
- website request
- remote host sensor or scheduled local watcher

When idle, durable agents should be dormant.

Dormancy means:

- no active long-lived reasoning loop by default
- no independent heartbeat by default
- no unnecessary process residency when there is no work

The parent heartbeat may notice stale durable-agent state, pending review artifacts, or missed wakeups.
But the parent heartbeat should not collapse into running every child's internal lifecycle.

## Channel Admission and Ingress Safety

Default rule:

**unconfigured external-channel attachment is inert.**

Examples:

- if the bot is added to an arbitrary group without prior durable-agent setup, it should be unable to affect the parent system
- if a public inbox receives mail before an email durable agent is configured, that mail should not enter ordinary heartbeat or prompt flow
- if a website route is exposed without a durable-agent charter, it should not write into the parent system

Admission into a durable-agent channel should require explicit setup/ritual at the admin layer.

## Outbound Autonomy

Durable agents may eventually send outward replies autonomously, but this must be explicit policy, not emergent drift.

Possible outbound modes:

- `read_only`
- `draft_only`
- `reply_with_parent_review`
- `reply_with_policy_authorization`

The key rule is:

outbound autonomy may be widened by admin ratification,
but it must never be implicitly granted by repeated external interaction.

## Example Use Cases

## Email durable agent

A user gives Aphelion an email address.
The system proposes creating a durable email agent.
The admin and parent define:

- whether the agent only reads or may ever send
- what kinds of messages to summarize
- what gets escalated
- what files are surfaced
- what is retained locally vs promoted upward

The email agent then polls or receives events, digests locally, and reports upward through review artifacts.
If the admin decides the summaries should change, that drift is approved in the parent conversation and then pushed downward.

## Family Telegram group durable agent

The bot being added to a family group should be inert by default.
Only after explicit durable-agent setup should the group become a live ingress surface.

The family durable agent may help locally in the group.
But if the group socially pressures the agent into a new standing role, that attempted drift is surfaced upward for admin review rather than becoming durable truth.

## Remote host durable agent

A copy of Aphelion may run on a remote host such as the admin's laptop.
That child may monitor files, processes, resource pressure, or local browser automation.

It still remains subordinate to the parent house.
Its privileges are bounded by a charter and capability envelope.
Behavior or privilege changes require admin-ratified drift from the parent side.

## Public website durable agent

A serverless or low-cost public-facing child may act as a website greeter or sales/signal collector.
This is the clearest hostile-ingress case.

Its charter should be narrow:

- public Q&A within bounds
- interest capture
- no parent mutation
- cheap model
- aggressive quarantine of transcripts and files

If the architecture is safe here, it becomes safe by construction in less-exposed cases.

## Remote Durable Agents

A durable agent may live on another host.
This does not change the constitutional model, but it adds transport and attestation requirements.

Minimum conceptual requirements:

- explicit parent/child registration
- host or runtime identity attestation
- scoped credentials or key exchange
- parent-known capability envelope
- explicit policy update path from parent to child
- bounded reporting path from child to parent

The remote child should not be treated as trusted merely because it is "ours".
Its authority still comes from the registered charter and enforced runtime policy.

## Isolation and Runtime Model

Go may orchestrate durable-agent runtimes, but Go alone is not the security boundary.

Security must be enforced at the OS/runtime level.

Required concepts:

- subprocess-per-run or equivalent isolated execution unit
- dedicated working roots
- explicit writable vs read-only mounts/roots
- dropped environment and hidden secret paths
- resource limits
- explicit network policy
- dormancy when idle

This may be implemented without Docker.
The design should depend on real Linux/host isolation primitives rather than container branding.

### Local vs remote

A durable agent on the same VPS and a durable agent on the admin's laptop may use different local runtimes.
The constitutional model remains the same:

- bounded charter
- isolated execution
- upward review artifacts
- no direct parent mutation

## Drift and Provenance

Any durable drift should record provenance including:

- which durable agent requested or motivated the change
- what external interaction or review artifact triggered review
- what admin decision ratified the change
- what policy/prompt/charter/capability changed
- when the change was pushed to the child

Durable agents must not acquire lasting changes silently.

## Relationship to Existing Specs

- `subagents.md` governs ordinary subordinate sessions and should later reference durable agents as a distinct subtype or sibling construct
- `heartbeat.md` governs the parent house heartbeat; durable agents default to source-triggered wakeups rather than independent heartbeats
- `security.md` governs isolation floors and secret boundaries; durable agents add external-ingress quarantine requirements on top
- `artifacts.md` and `artifact-brokerage.md` govern files and bounded review handling; durable-agent review artifacts should reuse those principles where possible
- `semantic-store.md` quarantine rules are conceptually aligned: external-channel corpora must not enter ordinary retrieval without explicit approval

## Decisions

- **Durable agents are quarantined organs, not independent selves.**
- **One heartbeat for the house.** Child agents wake from events or bounded polling by default.
- **No direct upward writes.** The normal upward path is a bounded review artifact.
- **Durable drift belongs to the admin conversation.** Outside interaction alone cannot ratify lasting change.
- **Trust widens local charter, not constitutional ownership.**
- **Remote-host children follow the same law.** New machine, same parent/child governance.
- **Public web agents are a proving ground, not an exception.** If hostile public ingress is safe, the quieter cases inherit that safety.

## Test Plan

- **TestDurableAgentCannotMutateParentPromptFromExternalInteraction**: child-local external events cannot directly rewrite parent prompt files
- **TestDurableAgentCannotMutateParentMemoryWithoutRatification**: upward promotion requires review and admin ratification
- **TestDurableAgentReportsUpwardThroughReviewArtifact**: parent sees bounded synthesis before raw transcript
- **TestRawDurableTranscriptRemainsSidecarInspectable**: full child transcript is inspectable but not default prompt input
- **TestOneHeartbeatForHouse**: durable agents do not own independent heartbeats by default
- **TestDurableAgentWakeIsEventOrPollDriven**: channel-appropriate wakeup paths function without child heartbeat loops
- **TestUnconfiguredGroupIngressIsInert**: adding the bot to a group without durable-agent setup has no effect on the parent house
- **TestEmailIngressDoesNotEnterHeartbeatDirectly**: unreviewed inbox content cannot reach ordinary parent heartbeat flow
- **TestAttemptedRoleDriftSurfacesForAdminReview**: social pressure from a group cannot durably redefine the child without ratification
- **TestOutboundAutonomyRequiresExplicitPolicy**: child reply autonomy is not implied by repeated use
- **TestRemoteDurableAgentReportsWithBoundedIdentityAndPolicy**: remote child reports under its registered charter and capability envelope
- **TestDurableDriftPreservesProvenance**: approved child changes record the motivating review artifact and admin ratification
