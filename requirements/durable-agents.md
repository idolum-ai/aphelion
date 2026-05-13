# Durable Agents — External Sensory Organs, Quarantine, and Parent Governance

## Overview

This document records the durable-agent design boundary. The current shipped
target is bounded local/scheduled/channel-facing durable children with
child-local state, scoped credentials, upward review, and parent-visible health.
Remote child control planes, public website agents, fleet management, and
marketplace-style durable-agent deployment are not current targets.

Aphelion needs a first-class model for **durable external-channel agents** only
where a concrete governed outpost workflow requires one.

This remains partly forward-looking and does not retroactively widen current
admission or security claims.

These agents are not ordinary task subagents.
They are persistent subordinate organs attached to the house through an external ingress surface such as:

- external_channel
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

The reference threat model for this architecture is **hostile public ingress**.

If the architecture safely contains a low-trust public website child, it should become safer by architectural default for quieter cases such as family groups, private inboxes, and trusted remote hosts.
Those quieter cases may widen local charter, but they should not bypass the same core quarantine and ratification laws.

## Telos

Durable agents should let Aphelion:

- attach to external channels safely
- preserve continuity within those channels
- execute bounded local actions
- synthesize important state upward to the parent
- remain steerable by the admin without becoming steerable by the public

They are sensory organs with local processing, not new sovereign selves.

## Scope

This spec defines the next durable-agent tranche, not the current platform floor.

The minimum security floor remains:

- config-admitted Telegram principals
- trusted-admin-first execution
- no broad public or semi-public ingress claims by default

### First durable-agent tranche

- a durable-agent concept distinct from ordinary subagents
- parent/child governance model for durable agents
- default quarantine boundary between child-local state and parent state
- bounded upward synthesis as review artifacts
- admin-ratified drift model for durable agent prompt/memory/behavior changes
- one parent heartbeat for the house; durable agents wake from source events or polling by default
- dormancy model for durable agents when idle
- isolation rules for local and remote durable-agent runtimes

### Deferred after the first durable-agent tranche

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

6. **Secrets are scoped by charter, not inherited by lineage.**
   A durable child must receive only the credentials and secret material required for its own charter. It must not inherit unrelated parent or sibling secrets by default.

7. **One heartbeat for the house.**
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
- `channel_kind` (`external_channel`, `telegram_group`, `telegram_dm`, `web_chat`, `remote_host`, etc.)
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

- digest inbound external-channel event and escalate important items
- act as a bounded family helper inside a Telegram group
- observe laptop resource pressure and take pre-approved remediations
- act as a public website greeter with no power to mutate the parent house

The charter is derivative of the parent house identity, but narrower and task-focused.

## External Actors Are Not House Principals

The people or systems encountered through a durable agent's channel are not house principals by default.

Examples:

- members of a Telegram group
- people sending external-channel events to a durable external channel
- visitors to a public website chat
- remote-host observations such as processes, files, sensors, or local machine state

These channel actors and observed inputs are child-local subjects of that durable agent, not `admin` or `approved_user` principals of the parent house.

That means:

- they do not inherit parent authority
- they do not get direct parent-session continuity
- they do not become eligible for tools or writable roots through channel contact alone
- they may influence child-local context, but only bounded child review artifacts may move upward

If a future architecture introduces broader cross-transport identity, it should do so explicitly in the principal model rather than implicitly through durable-agent ingress.

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

## Secret Boundary and Exfiltration Resistance

Durable-agent safety must hold even when the child is socially compromised.

Assume an external actor successfully convinces the durable agent to do something unsafe.
The architecture should still prevent broad house compromise by constraining what the child can materially see and send.

### Least-secret law

A durable agent must receive only the secrets required for its own charter.

Examples:

- an external-channel child may receive external-channel credentials for that external channel
- a remote-host child may receive host-local credentials required for its charter
- a public website child should generally receive no parent-house secrets beyond what its own runtime minimally requires

A durable external-channel agent should not be able to leak unrelated parent credentials if those credentials are outside its scoped secret surface.

### No inherited parent secret surface

A durable child must not inherit by default:

- parent host environment variables
- unrelated API keys
- sibling durable-agent credentials
- admin CLI auth material
- global credential files merely because they exist on the same machine

Lineage does not imply secret inheritance.

### Untrusted upward secret requests

Requests flowing upward from a durable child for:

- credentials
- capability widening
- tool enablement
- policy relaxation

must be treated as untrusted review material, not as authenticated admin intent.

If a phished child reports upward, for example, "the operator asked me to send the deployment credentials," the parent must surface that as suspicious child-originated review content rather than comply as if the admin had issued the request directly.

### Secret scrubbing on upward synthesis

Review artifacts, review events, and surfaced child syntheses should redact or quarantine suspected secret material rather than casually propagate it upward.

The upward path is for bounded review, not for secret exfiltration through summarization.

That means:

- bounded parent review artifacts should contain redacted summaries rather than exact secret-bearing payloads
- if exact material must be retained for audit or incident response, it should live in restricted forensic sidecar storage rather than ordinary review content
- inspection of that restricted sidecar should require an explicit admin-only path
- secret-like material that is neither needed for review nor safe to retain may be dropped entirely instead of being propagated

## Local Accommodation vs Durable Drift

Durable agents may accommodate local conversation within their current envelope.

Examples:

- a family group agent may answer in a warmer tone inside the group
- an external-channel child may summarize a PDF for the current review cycle
- a website greeter may adapt to the visitor's question within its public sales/support charter

But local accommodation is not durable drift.

Durable drift includes:

- changing summary shape or escalation policy long-term
- adopting a new standing role (for example, "you are now my software developer")
- enabling outbound replies where they were not allowed
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

The normal upward conduit should reuse the existing review events, artifact retention/quarantine rules, and operator-ratification discipline rather than inventing a parallel review plane.

At minimum, that means:

- the child-local synthesis becomes a bounded parent review artifact
- surfaced files or corpora remain artifacts with explicit retention and quarantine semantics
- raw child transcripts remain sidecar-inspectable state
- promotion into broader parent memory or retrieval still requires the existing ratification/review path

The parent house reviews that artifact first.
The parent may then send an admin-facing synthesis for final ratification before broader retention or reframing.

### Raw transcript discipline

Full durable-agent transcripts should remain inspectable sidecar state, not ordinary parent prompt input.

The parent may inspect them explicitly when needed.
They should not be the default upward path.

## Memory Model

Durable-agent memory should not be treated as one undifferentiated store.

At minimum, the system should distinguish between:

- **policy memory** — charter, capability envelope, outbound mode, drift policy
- **continuity memory** — the local and parent-reviewed state that gives the child continuity over time
- **inference memory** — transient runtime state such as prefix caches, KV caches, or other reusable prefill artifacts

These classes serve different purposes and must not collapse into one promotion path.

### Policy memory

Policy memory defines what the child is allowed to do.

It includes:

- charter
- capability envelope
- outbound mode
- drift policy
- ratified standing constraints

Policy memory flows downward after admin-ratified change.
It must not be learned durably from external interaction alone.

### Continuity memory

Continuity memory defines what the child carries forward as local reality across wakeups.

It may include:

- recent channel context
- local summaries
- cursors and pending work
- source-specific observations
- bounded local preferences that are explicitly allowed within the charter
- references to parent review artifacts
- distilled outcomes from parent review
- memory candidates

Continuity memory is the main surface where external contact can become durable local continuity.
It therefore requires quarantine, bounded retention, and selective upward-promotion discipline.
It is not the same as policy memory, and it should not be governed as if it were a downward-only policy layer.

### Inference memory

Inference memory is transient runtime optimization state, not durable memory.

Examples include:

- reusable shared prefixes
- KV cache or other attention-state reuse
- embedding or prefill cache reuse across matching runs

Inference memory should be treated as:

- transient
- runtime-owned
- architecture-conditional
- optimization-oriented rather than identity-bearing

Inference memory must not be treated as durable semantic truth, policy, or ordinary memory promotion material.

### Inference-state reuse across children

When multiple children process the same public prefix, the runtime may eventually support inference-state reuse across matching models for performance.

This is a systems optimization, not a constitutional memory-sharing path.

It should only be considered when:

- the model family or architecture matches
- the serving layer actually exposes such reuse control
- the reused prefix belongs to a surface that is allowed to be shared

This does **not** imply that one child's private state becomes another child's memory.

### Public-prefix vs private-state rule

The clean rule is:

- shared public context may be eligible for shared inference-state reuse
- child-private state is not

Examples:

- a public poker table transcript may be a shared runtime prefix
- a player's hole cards are not
- a player's private tactical report is not
- hidden beliefs about other players are not

Parent-mediated relay still defines what becomes public context.
The optimization layer must follow that boundary, not widen it.

### Remote or API limitations

Inference-state reuse is not a universal assumption.

It may be unavailable when:

- children use unrelated model families
- the serving provider does not expose runtime cache control
- the child runs over a generic hosted API surface where KV or prefix reuse is opaque

For example, OpenRouter-style API routing may support child model selection without exposing the low-level inference-state controls needed for cross-child cache reuse.

That means inference memory should be specified as an optional runtime optimization layer, not as a required part of durable-agent semantics.

## Memory Zones

The system should treat durable-agent state as three zones.

The memory layers above and the zones below are orthogonal, not competing classifications.

The intended mapping is:

- bootstrap policy ceilings live locally on the child host
- parent-authored live policy normally lives in admin-ratified durable memory and is then applied downward within the local ceiling
- continuity memory normally lives in child-local working memory, with bounded references to parent review artifacts
- parent review artifacts remain their own zone and should not be absorbed wholesale into continuity memory by default
- inference memory remains a runtime-owned optimization layer outside the durable memory zones

### 1. Child-local working memory

Used by the durable agent to do its job.
This may include:

- recent channel context
- local summaries
- pending actions
- source-specific metadata
- cursors and other operational continuity state

This state is not automatically trusted by the parent.
It belongs primarily to the continuity-memory layer, not to shared house memory.

### 2. Parent review artifact

The synthesized upward artifact the parent sees first.
This is the normal review surface between child and parent.
Child-local continuity may keep references, distilled outcomes, or pending-status pointers to these artifacts, but the artifacts themselves should remain separate bounded objects for audit, retention, and deletion discipline.

### 3. Admin-ratified durable memory

Only after parent review and admin ratification should information be promoted into broader parent memory, policy, or retrieval surfaces.

This keeps the external world from writing directly into the center of the house.

### Forgetting and retention ceilings

Durable-agent memory must support forgetting as a first-class behavior.

If every child keeps everything indefinitely, the architecture becomes porous, expensive, and vulnerable to accidental drift.

At minimum, child-local continuity memory should support:

- bounded windows
- retention ceilings
- decay or compaction
- explicit local deletion policies

Promotion upward should remain selective and asymmetric:

- easy to keep local for a while
- harder to promote upward durably
- possible to forget locally without affecting parent memory

## Wakeup and Heartbeat Model

The default shape is:

- **one heartbeat for the house**
- **event- or poll-driven wakeups for durable agents**

Durable agents should wake through source-appropriate mechanisms:

- inbound external-channel event poll or push notification
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

## Conversational Creation and Child Charter

Durable agents should not be registry-only objects created out-of-band forever.

The admin conversation with `Idolum` should be able to create a new bounded child through ordinary dialogue.

The shape is:

1. the parent proposes creating a durable child
2. the admin approves the proposal
3. the conversation becomes a bounded setup flow
4. the resulting child charter is persisted as machine-readable policy
5. activation happens only after the required channel connection is configured

This should feel more like setting up a specialist worker than filling out a hidden config file.

The conversation may ask one missing question at a time, for example:

- should the child be read-only at first
- should it ever draft or send replies
- what counts as important enough to surface upward
- whether PDFs or other attachments should be summarized automatically
- how often the child should synthesize findings upward
- whether it wakes on polling, push, or both
- what local actions are allowed
- what should never be retained

The answers belong to the charter formed by the admin and parent together.
They do not come from external senders or from the child improvising its own standing role.

## Capability Delegation Contract

Authorization alone is not enough. Durable governance should include a
child-legible delegation contract for permissions that exceed the child's
current charter.

Minimum delegation loop:

1. Child or parent creates a `capability_request` with kind, target resource,
   requested principal, purpose, and proposed constraints.
2. Parent review is recorded when a parent principal is named.
3. Admin review approves or rejects the request.
4. Any required provisioning or attestation happens through the relevant
   lifecycle surface, such as external-tool install/audit/probe/verify.
5. Admin creates an active `capability_grant` with allowed actions, expiration,
   policy fingerprint, and constraints.
6. Runtime invocation checks the grant and records allowed or failed use.

The registry/runtime should retain these facts as durable machine-readable state,
and `/status` should expose request/grant state, stale reason, policy anchor,
invocation counters, and failure counters.

## Registry Shape

The durable-agent registry should keep using the existing durable-agent record as the durable identity and policy object.

The first structured extension should be:

- `channel_config`: machine-readable channel-specific configuration for the child

This should not replace the existing live policy.
The split is:

- `live_policy`: behavioral authority and constitutional posture
- `channel_config`: source-specific wiring and bounded child charter details

For an external-channel child, the top-level durable-agent fields should continue to hold:

- `channel_kind=external_channel`
- `wakeup_mode`
- `secret_scopes`
- `local_storage_roots`
- `network_policy`
- `live_policy.outbound_mode`
- `live_policy.capability_envelope`

The channel-specific `channel_config` should hold the rest of the operational shape, such as:

- owned address or external-channel identity
- adapter kind, for example `codex_app_server` or a future email adapter
- poll interval
- importance / escalation criteria
- attachment handling rules
- synthesis cadence
- retention ceilings or explicit `never_retain` classes

Runtime continuity for a live external channel belongs in the generic `external_channel` state slot, not in adapter-specific top-level continuity fields. That state records the adapter, cursor/session reference, last command, attempt/success timestamps, artifact pointer, status/error, failure count, backoff, and opaque `adapter_state` for protocol-specific residue.

Adapter operations are command vocabularies governed by policy and grants. Examples include read-only status heartbeat, message search, thread fetch, or approval-callback handling. A prompt or query string may be carried inside a command, but it is not authority by itself.

This keeps the child charter structured without flattening all durable-agent behavior into one giant generic policy object.

## Channel Admission and Ingress Safety

Default rule:

**unconfigured external-channel attachment is inert.**

Examples:

- if the bot is added to an arbitrary group without prior durable-agent setup, it should be unable to affect the parent system
- if a public external channel receives external-channel events before an external-channel durable agent is configured, that mail should not enter ordinary heartbeat or prompt flow
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

These are not just thematic sketches. They are intended execution shapes for the first durable-agent tranche.

## Email durable agent

A user gives Aphelion an external channel address.
The system proposes creating a durable external-channel agent.
The admin and parent define:

- whether the agent only reads or may ever send
- what kinds of messages to summarize
- what gets escalated
- what files are surfaced
- what is retained locally vs promoted upward

The external-channel child then polls or receives events, digests locally, and reports upward through review artifacts.
If the admin decides the summaries should change, that drift is approved in the parent conversation and then pushed downward.

### Conversational setup

The preferred creation path is a guided parent-admin conversation, not a startup-only config entry.

Typical flow:

1. Proposal:
   `Idolum` proposes creating an external-channel durable child and asks for approval before capability or connection work begins.
2. Setup questions:
   The parent asks only the missing questions needed to form a bounded charter.
   The setup surface should be an explicit wizard state machine with durable fields such as:
   - `status` (`in_progress`, `ready`, `finalized`, `cancelled`)
   - `current_step`
   - `missing` answer list
   - machine-readable `answers`
3. Draft persistence:
   The registry persists the child in a `draft` or other non-active state while the charter is still being formed.
   Wizard state must survive process restart so setup can resume without re-asking completed steps.
4. Connection/readiness check:
   The parent verifies generic prerequisites it owns: the child identity, bounded charter, scoped grants, and runtime materialization envelope. Adapter-specific live probes are not hard-coded into Aphelion for each child feature; when a child needs adapter repair or a new probe, it requests that through parent conversation and a governed proposal.
5. Activation:
   The child becomes `active` only once the charter and the generic readiness contract are ready. Channel-specific work then happens in the child environment and reports upward through review artifacts.

The public surface should remain one coherent `Idolum` conversation, even when the underlying runtime is drafting structured child policy behind the scenes.

### Typical flow

1. Admin setup:
   The parent house defines the external-channel child's charter, retention ceiling, outbound mode, and escalation rules.
2. Registration:
   The durable-agent registry stores the child identity, scoped external-channel credentials, wakeup mode, and local working roots.
3. Email ingress:
   The channel adapter receives or polls a new message and normalizes body text, metadata, and attachments into child-local artifacts.
4. Child-local processing:
   The child classifies urgency, extracts allowed document content, enforces `never_retain` scrubbing rules, and decides whether the message is routine, escalatory, or drift-seeking.
   `never_retain` enforcement should be explicit in review metadata (for example with redaction counters or risk flags).
5. Local action:
   If policy allows it, the child may prepare a draft reply or take another bounded local action.
6. Upward synthesis:
   The child emits a bounded review artifact summarizing what happened, what it did locally, what files matter, and any drift candidates or suspicious requests.
   If `synthesis_cadence` is configured, review emission is cadence-gated and intermediate external-channel observations are buffered in durable child state until the cadence window opens.
7. Parent review:
   The parent house sees the bounded artifact first, not the raw external-channel transcript.
8. Admin ratification:
   Any durable change to summary policy, outbound autonomy, or promoted memory must be approved in the admin conversation.
9. Downward update:
   Approved policy or charter changes are pushed back to the child with provenance.
10. Dormancy:
   The child returns to idle until the next external-channel event or bounded poll cycle.

### First live slice

The first concrete slice should be intentionally narrow:

- read-only external-channel child
- wakeups through polling, push, or both (`poll_or_push`)
- no outbound mail
- scoped external-channel credentials for the child only
- bounded upward digests through review artifacts

This slice is already enough to make the architecture real:

- the external channel has continuity
- the child owns the local work
- the parent remains protected
- widening autonomy later still goes through admin-ratified drift

## Family Telegram group durable agent

The bot being added to a family group should be inert by default.
Only after explicit durable-agent setup should the group become a live ingress surface.

The family durable agent may help locally in the group.
But if the group socially pressures the agent into a new standing role, that attempted drift is surfaced upward for admin review rather than becoming durable truth.

### Typical flow

1. Admission:
   Adding the bot to a group does nothing until the admin explicitly creates a durable group child.
2. Chartering:
   The parent defines what the child may do in the group, what tone latitude it has, and whether it may ever reply autonomously.
3. Group ingress:
   Mentions or messages wake only the group child, not the parent house directly.
4. Child-local continuity:
   The child keeps recent group context and may answer within its charter.
5. Drift detection:
   If the group repeatedly pressures the child into a new standing role or policy, the child treats that as attempted durable drift rather than accommodation.
6. Upward synthesis:
   The child emits a bounded artifact summarizing important interactions, family-relevant questions, and any drift attempts.
7. Parent/admin review:
   The parent surfaces the group synthesis in the admin conversation for decision.
8. Ratified update:
   Only an admin-ratified change may widen the child's standing role, autonomy, or memory policy.
9. Ongoing dormancy:
   Between group events, the child remains dormant rather than running an internal heartbeat loop.

## Remote host durable agent

A copy of Aphelion may run on a remote host such as the admin's laptop.
That child may monitor files, processes, resource pressure, or local browser automation.

It still remains subordinate to the parent house.
Its privileges are bounded by a charter and capability envelope.
Behavior or privilege changes require admin-ratified drift from the parent side.

### Typical flow

1. Registration:
   The parent house registers the remote child with host identity, attested runtime, charter, and capability envelope.
2. Scoped provisioning:
   The remote child receives only the host-local credentials and writable roots required for its own charter.
3. Local observation:
   Source events such as file changes, process state, battery pressure, or local browser state wake the child.
4. Child-local reasoning:
   The child interprets those observations within its charter.
5. Routine local action:
   The child may take only the narrow class of routine local actions that were explicitly pre-authorized in its charter.
6. Privileged or standing change requests:
   Actions that widen privilege, change standing policy, or cross the child's routine charter must be surfaced upward for parent/admin review rather than executed locally.
7. Sensitive boundary:
   Host observations remain child-local inputs, not house principals and not direct parent prompt content.
8. Upward synthesis:
   The child reports status, anomalies, completed local actions, and requested changes upward through bounded review artifacts.
9. Parent review:
   The parent decides whether any requested privilege change, tooling expansion, or standing policy drift should be ratified.
10. Downward sync:
   Approved changes are pushed to the remote child explicitly with provenance.

## Public website durable agent

A serverless or low-cost public-facing child may act as a website greeter or sales/signal collector.
This is the clearest hostile-ingress case.

Its charter should be narrow:

- public Q&A within bounds
- interest capture
- no parent mutation
- cheap model
- aggressive quarantine of transcripts and files

If the architecture is safe here, the less-exposed cases should become safer by architectural default.

### Typical flow

1. Explicit deployment:
   The website route stays inert until the admin creates a durable web child with a narrow charter.
2. Hostile ingress:
   Public visitors interact only with the child, never with the parent house directly.
3. Local processing:
   The child answers bounded public questions, captures permitted contact signals, and normalizes uploaded files into child-local artifacts.
4. Aggressive quarantine:
   Public transcripts, uploads, and extracted text stay quarantined by default and must not enter ordinary parent retrieval or memory.
5. Escalation filtering:
   The child emits bounded upward artifacts only for meaningful sales, support, anomaly, or drift-relevant cases.
6. Parent review:
   The parent sees redacted synthesized review content first and may inspect sidecar material explicitly if needed.
7. Policy pressure:
   Repeated public attempts to widen authority or obtain secrets are treated as hostile pressure, not as legitimate product steering.
8. Admin ratification:
   Any widening of outbound behavior, memory promotion, or website charter occurs only through the admin conversation.

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

## Remote Child–Parent Control Plane

Remote durable agents require an explicit control-plane protocol.

This protocol is not yet implemented in the current runtime.

The preferred first transport shape is:

- child-initiated secure connection to the parent control plane
- TLS for all transport, plus signed application-level envelopes for identity and message integrity
- parent-signed policy or configuration updates
- child acknowledgements of the exact applied policy version and hash

This should normally be implemented as:

- a persistent child-initiated connection when the runtime supports it
- request/response artifact upload plus policy polling when persistent connectivity is unavailable or undesirable

### Why child-initiated transport

Child-initiated transport is the preferred default because it:

- works behind NAT and home-network constraints
- avoids requiring inbound ports on child hosts
- fits dormant or intermittently waking children
- maps cleanly to remote laptops, VPS children, and serverless runtimes

### Transport shape

For long-lived children on ordinary hosts, the preferred transport is:

- child-initiated secure bidirectional connection over an HTTPS-shaped transport

For ephemeral or serverless children, the preferred degraded transport is:

- signed artifact upload over HTTPS
- signed policy polling over HTTPS

The control-plane semantics should remain the same across both shapes even if the transport differs.

### Envelope semantics

The control-plane envelope should preserve:

- protocol version
- child agent identity
- parent or house identity
- message kind
- message id
- timestamp
- payload
- signature
- mandatory replay-protection field such as sequence number, nonce, or bounded timestamp window

Supported message kinds should eventually include:

- enrollment or re-attestation
- review artifact upload
- child state update
- policy poll
- policy update
- policy acknowledgement
- key rotation or revocation notice

### Replay protection and ordering

Replay protection is required, not optional.

The control plane should reject:

- duplicate message ids
- invalid signatures
- envelopes outside the allowed replay window
- stale or out-of-order policy updates

### Policy version semantics

Every live policy should carry at least:

- `policy_version`
- `policy_hash`
- `issued_at`

The child acknowledgement should report:

- which policy version was received
- which version was applied
- whether application succeeded or failed

The parent should be able to distinguish:

- last policy offered
- last policy acknowledged
- last policy actually applied

## Configuration Split

Remote durable-agent configuration should be split into two layers.

### 1. Local bootstrap configuration

Bootstrap configuration lives on the child host and is installed by the operator or host owner.

It should define at least:

- parent control-plane URL
- child agent id
- enrollment credential or one-time enrollment token
- local key material or key-registration path
- child-local node LLM backend
- allowed model or provider options available on that host
- local writable roots
- local secret availability
- local network or infrastructure ceiling

The local bootstrap configuration defines the maximum local ceiling.

The parent must not silently exceed that ceiling through later policy updates.

For LLM-backed children, the bootstrap should also define the node's own inference/auth path.

At minimum, the child bootstrap should be able to express:

- `backend = native`
  - `native_provider = anthropic | openai | openrouter | gemini | ollama`
  - child-local API key
  - optional base URL, model, max tokens
- `backend = codex`
  - child-local Codex auth source
  - child-local `codex_home`
  - optional child-local Codex base URL

The key rule is:

- child-local LLM bootstrap is secret-bearing bootstrap state
- it is not part of parent-authored live policy
- it must not be inherited ambiently from the parent merely because the child runs on the same machine

### Bootstrap ceiling law

Parent-authored live policy may narrow the local bootstrap ceiling.
It must not widen that ceiling.

Widening the ceiling requires explicit local operator action on the child host.

### 2. Parent-authored live policy

After enrollment, the parent may supply signed live policy describing the child's behavioral charter.

This may include:

- role or charter
- model or provider selection within the locally allowed ceiling
- capability envelope
- outbound mode
- drift policy
- review cadence or wakeup policy
- task-specific behavioral constraints

The child should apply only parent policy that is validly signed and remains within the local bootstrap ceiling.

Parent live policy may choose within the locally allowed node LLM ceiling, but it must not carry new secret-bearing credentials for that child.

### Parent-declared public surface

Whether a surface counts as public for shared inference-state reuse should be decided in the admin-parent conversation, then encoded into parent-authored live policy.

Conversation alone is not enough.
The runtime needs a stable machine-readable declaration to enforce.

At minimum, the live policy should be able to express something like:

- `public_surface_mode = none | channel_transcript | explicit_parent_relay_only`
- `shared_inference_reuse = disabled | allowed`
- `shared_inference_reuse_scope = public_prefix_only`

That means:

- the admin and parent may discuss and ratify what counts as public
- the child should only treat that decision as effective after it appears in valid signed live policy
- private child state remains private even if it was discussed conversationally, unless it is explicitly reclassified through policy

## Enrollment, Rotation, and Revocation

The remote control plane should support at least:

- first enrollment
- later re-attestation
- key registration
- key rotation
- parent-side revocation or decommissioning

A decommissioned or revoked child must not continue to receive privileged live policy merely because it still has older local state.

## Parent and Child Model Configuration

The parent and child may use different models and providers.

This should be supported explicitly.

Examples include:

- parent on one provider or model family
- child on OpenRouter with a specified open model
- child on Codex using its own local Codex auth/home
- multiple children using different models for the same experiment

For durable children created directly by the parent on infrastructure under parent or admin control, the parent may provision the child configuration automatically.

For children installed on independently managed hosts, the operator should install bootstrap config locally, then let the child enroll outward to the parent.

### Implemented local child bootstrap

The current same-host Telegram durable-child implementation already follows this law in a minimal form:

- the durable-agent record persists a child-local node LLM bootstrap separate from live policy
- the child runtime builds its config from that bootstrap
- native children use only their own native provider credentials
- Codex children use only their own Codex auth/home settings
- Codex children currently use floor-fallback serialization inside the child turn rather than a child-local face-provider render path
- the child must not silently fall back to the parent's LLM credentials if its own bootstrap is missing or unusable

## Offline and Dormant Semantics

A remote child may be:

- offline
- intermittently connected
- dormant between source events

While disconnected, the child may:

- continue only within its last valid applied policy
- queue bounded signed review artifacts locally for later upload

While disconnected, the child must not:

- infer broader authority from parent unreachability
- self-widen local ceilings
- assume missing parent contact is implicit approval

When connectivity resumes, queued artifacts should upload and policy reconciliation should occur explicitly.

## Parent-Mediated Multi-Agent Communication

Durable children should not trust or reconfigure each other directly by default.

The preferred rule is:

- child-to-child communication routes through the parent control plane unless an explicitly ratified topology says otherwise
- artifacts crossing between children remain bounded, provenance-bearing, and policy-governed
- one child must not widen another child's authority or secret surface
- any shared runtime prefix optimization may follow only the parent-authored live-policy declaration of public surface, not private child state

If later direct child-to-child communication is allowed, it should still preserve:

- explicit topology
- signed envelopes
- provenance
- policy ceilings
- parent-observable auditability

## Isolation and Runtime Model

Go may orchestrate durable-agent runtimes, but Go alone is not the security boundary.

Security must be enforced at the OS/runtime level.

Required concepts:

- subprocess-per-run or equivalent isolated execution unit
- dedicated working roots
- explicit writable vs read-only mounts/roots
- dropped environment and hidden secret paths
- charter-scoped credential mounting or injection
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

- `principals.md` still governs the current house principal model; durable-agent channel actors are not house principals by default
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
- **Secrets are least-privilege and child-scoped.** A durable child should only be able to leak what it can materially access.
- **Child-originated secret requests are untrusted.** Upward requests for credentials or capability widening must enter review, not execution.
- **Remote-host children follow the same law.** New machine, same parent/child governance.
- **Routine local action and standing privilege are different.** Pre-authorized local remediation does not imply self-widening authority.
- **Continuity memory and inference memory are different layers.** Shared runtime prefix optimization is not shared durable memory.
- **Only public context may become shared inference state.** Private child state must not be co-mingled into sibling runtime reuse surfaces.
- **Hostile public ingress is the reference threat model.** If the public web case is safe, the quieter cases inherit that safety.

## Test Plan

- **TestDurableAgentCannotMutateParentPromptFromExternalInteraction**: child-local external events cannot directly rewrite parent prompt files
- **TestDurableAgentCannotMutateParentMemoryWithoutRatification**: upward promotion requires review and admin ratification
- **TestDurableAgentReportsUpwardThroughReviewArtifact**: parent sees bounded synthesis before raw transcript
- **TestExternalChannelActorsDoNotBecomeHousePrincipals**: group members, external-channel senders, website visitors, and remote observations remain child-local actors unless explicitly admitted through the principal model
- **TestDurableAgentUpwardSynthesisUsesExistingReviewConduit**: upward child synthesis enters the parent through the existing review/artifact/quarantine path rather than a parallel review subsystem
- **TestDurableAgentSecretsAreCharterScoped**: a child receives only the credentials required for its own charter, not unrelated parent or sibling secrets
- **TestChildCannotExfiltrateUnavailableParentSecret**: a socially compromised child cannot leak a secret that is outside its scoped secret surface
- **TestChildOriginatedCredentialRequestEntersReviewNotExecution**: upward requests for credentials or capability widening from a durable child are treated as suspicious review material rather than authenticated admin instruction
- **TestUpwardSynthesisRedactsOrQuarantinesSecretMaterial**: secret-like strings discovered by a child are redacted or quarantined on the upward review path
- **TestRawDurableTranscriptRemainsSidecarInspectable**: full child transcript is inspectable but not default prompt input
- **TestOneHeartbeatForHouse**: durable agents do not own independent heartbeats by default
- **TestDurableAgentWakeIsEventOrPollDriven**: channel-appropriate wakeup paths function without child heartbeat loops
- **TestUnconfiguredGroupIngressIsInert**: adding the bot to a group without durable-agent setup has no effect on the parent house
- **TestExternalChannelIngressDoesNotEnterHeartbeatDirectly**: unreviewed external-channel content cannot reach ordinary parent heartbeat flow
- **TestAttemptedRoleDriftSurfacesForAdminReview**: social pressure from a group cannot durably redefine the child without ratification
- **TestOutboundAutonomyRequiresExplicitPolicy**: child reply autonomy is not implied by repeated use
- **TestRemoteDurableAgentReportsWithBoundedIdentityAndPolicy**: remote child reports under its registered charter and capability envelope
- **TestRemoteRoutineActionDoesNotImplyPrivilegeWidening**: a remote child may perform pre-authorized routine remediations without gaining self-directed authority expansion
- **TestRemoteControlPlaneUsesTLSAndSignedEnvelopes**: remote control-plane transport requires TLS plus valid application signatures
- **TestRemoteControlPlaneRejectsReplay**: duplicate or replayed envelopes are rejected
- **TestRemotePolicyAckCarriesAppliedVersion**: parent can observe offered vs acknowledged vs applied policy version
- **TestParentPolicyCannotExceedBootstrapCeiling**: live parent policy may narrow but not widen local bootstrap ceilings
- **TestRemoteChildQueuesArtifactsWhileOfflineWithoutAuthorityExpansion**: offline children may queue bounded artifacts but may not infer broader authority
- **TestParentMediatedChildToChildCommunicationPreservesProvenance**: inter-child artifacts remain parent-mediated and bounded unless explicitly ratified otherwise
- **TestSharedInferenceStateUsesOnlyPublicPrefix**: runtime prefix or KV reuse, when supported by the serving/runtime layer, may use declared public context but not private child state
- **TestInferenceStateReuseDoesNotPromoteDurableMemory**: transient runtime cache reuse, when supported, does not create shared semantic memory or house-memory promotion
- **TestMismatchedModelFamiliesSkipSharedInferenceReuse**: children using mismatched architectures or opaque hosted APIs do not assume cross-child inference-state reuse even if the optimization exists elsewhere
- **TestHostilePublicIngressIsContainedByQuarantine**: public website pressure cannot bypass the durable-agent quarantine and ratification boundary
- **TestDurableDriftPreservesProvenance**: approved child changes record the motivating review artifact and admin ratification
