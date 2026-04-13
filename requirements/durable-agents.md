# Durable Agents — External Sensory Organs, Quarantine, and Parent Governance

## Overview

Aphelion needs a first-class model for **durable external-channel agents**.

This is a forward-looking architecture spec for the next major tranche after the current DM-only principal floor.
It does not retroactively widen the existing `v0` admission or security claims.

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

This spec defines the next durable-agent tranche, not the current platform floor.

The current floor remains:

- DM-only Telegram principals
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

## External Actors Are Not House Principals

The people or systems encountered through a durable agent's channel are not house principals by default.

Examples:

- members of a Telegram group
- people sending mail to a durable inbox
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

- an email child may receive inbox credentials for that inbox
- a remote-host child may receive host-local credentials required for its charter
- a public website child should generally receive no parent-house secrets beyond what its own runtime minimally requires

A durable email agent should not be able to leak unrelated parent credentials if those credentials are outside its scoped secret surface.

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

If a phished child reports upward, for example, "Daniel asked me to send the deployment credentials," the parent must surface that as suspicious child-originated review content rather than comply as if the admin had issued the request directly.

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

These are not just thematic sketches. They are intended execution shapes for the first durable-agent tranche.

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

### Typical flow

1. Admin setup:
   The parent house defines the email child's charter, retention ceiling, outbound mode, and escalation rules.
2. Registration:
   The durable-agent registry stores the child identity, scoped inbox credentials, wakeup mode, and local working roots.
3. Email ingress:
   The email adapter receives or polls a new message and normalizes body text, metadata, and attachments into child-local artifacts.
4. Child-local processing:
   The child classifies urgency, extracts allowed document content, and decides whether the message is routine, escalatory, or drift-seeking.
5. Local action:
   If policy allows it, the child may prepare a draft reply or take another bounded local action.
6. Upward synthesis:
   The child emits a bounded review artifact summarizing what happened, what it did locally, what files matter, and any drift candidates or suspicious requests.
7. Parent review:
   The parent house sees the bounded artifact first, not the raw inbox transcript.
8. Admin ratification:
   Any durable change to summary policy, outbound autonomy, or promoted memory must be approved in the admin conversation.
9. Downward update:
   Approved policy or charter changes are pushed back to the child with provenance.
10. Dormancy:
   The child returns to idle until the next inbox event or bounded poll cycle.

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
   The child interprets those observations within its charter and may take bounded local actions if pre-authorized.
5. Sensitive boundary:
   Host observations remain child-local inputs, not house principals and not direct parent prompt content.
6. Upward synthesis:
   The child reports status, anomalies, completed local actions, and requested changes upward through bounded review artifacts.
7. Parent review:
   The parent decides whether any requested privilege change, tooling expansion, or standing policy drift should be ratified.
8. Downward sync:
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

If the architecture is safe here, it becomes safe by construction in less-exposed cases.

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
- **Public web agents are a proving ground, not an exception.** If hostile public ingress is safe, the quieter cases inherit that safety.

## Test Plan

- **TestDurableAgentCannotMutateParentPromptFromExternalInteraction**: child-local external events cannot directly rewrite parent prompt files
- **TestDurableAgentCannotMutateParentMemoryWithoutRatification**: upward promotion requires review and admin ratification
- **TestDurableAgentReportsUpwardThroughReviewArtifact**: parent sees bounded synthesis before raw transcript
- **TestExternalChannelActorsDoNotBecomeHousePrincipals**: group members, inbox senders, website visitors, and remote observations remain child-local actors unless explicitly admitted through the principal model
- **TestDurableAgentUpwardSynthesisUsesExistingReviewConduit**: upward child synthesis enters the parent through the existing review/artifact/quarantine path rather than a parallel review subsystem
- **TestDurableAgentSecretsAreCharterScoped**: a child receives only the credentials required for its own charter, not unrelated parent or sibling secrets
- **TestChildCannotExfiltrateUnavailableParentSecret**: a socially compromised child cannot leak a secret that is outside its scoped secret surface
- **TestChildOriginatedCredentialRequestEntersReviewNotExecution**: upward requests for credentials or capability widening from a durable child are treated as suspicious review material rather than authenticated admin instruction
- **TestUpwardSynthesisRedactsOrQuarantinesSecretMaterial**: secret-like strings discovered by a child are redacted or quarantined on the upward review path
- **TestRawDurableTranscriptRemainsSidecarInspectable**: full child transcript is inspectable but not default prompt input
- **TestOneHeartbeatForHouse**: durable agents do not own independent heartbeats by default
- **TestDurableAgentWakeIsEventOrPollDriven**: channel-appropriate wakeup paths function without child heartbeat loops
- **TestUnconfiguredGroupIngressIsInert**: adding the bot to a group without durable-agent setup has no effect on the parent house
- **TestEmailIngressDoesNotEnterHeartbeatDirectly**: unreviewed inbox content cannot reach ordinary parent heartbeat flow
- **TestAttemptedRoleDriftSurfacesForAdminReview**: social pressure from a group cannot durably redefine the child without ratification
- **TestOutboundAutonomyRequiresExplicitPolicy**: child reply autonomy is not implied by repeated use
- **TestRemoteDurableAgentReportsWithBoundedIdentityAndPolicy**: remote child reports under its registered charter and capability envelope
- **TestDurableDriftPreservesProvenance**: approved child changes record the motivating review artifact and admin ratification
