# Durableagent Product Contract

`durableagent/` is the durable child-agent substrate: it lets child agents
exist, sync, report, and preserve local continuity without inheriting parent
identity, memory, credentials, or authority.

This document is the product-contract inventory for bringing the package to
5/5 definition, test, and documentation maturity. It is intentionally separate
from implementation details: higher layers still decide policy, authority,
operator review, deployment, and memory promotion.

## Product boundary

`durableagent/` owns:

- child-agent archetype loading and validation
- remote bootstrap read/write
- provision-plan construction and remote child installation payloads
- child-local workspace/memory root derivation
- signed control-plane envelopes and replay-aware HTTP transport
- enrollment, re-attestation, policy polling, policy acknowledgement, parent
  conversation polling/acknowledgement, and review artifact upload
- parent-side review artifact queueing and forensic/redaction handling
- remote child sync/run/loop helpers
- snapshot create/list/load/restore/migration for child workspace and memory

`durableagent/` does **not** own:

- granting authority to a child
- deciding live child policy semantics
- promoting child memory into parent memory
- deciding parent/operator approval
- creating public-contact authority
- deploying, restarting, or merging changes
- becoming channel-specific application logic for one child

The invariant is:

> `durableagent/` may move, validate, contain, and record child-agent data; it
> may not grant authority or change parent policy by itself.

## 5/5 maturity rubric

A product-level feature is **defined 5/5** when its purpose, callers, inputs,
outputs, state effects, authority boundary, rejection modes, and ownership seam
are explicit.

A product-level feature is **tested 5/5** when it has relevant happy-path,
invalid-input, security/boundary, persistence/restart/idempotency, and store or
transport failure coverage.

A product-level feature is **documented 5/5** when operator-facing and
maintainer-facing docs explain what the feature does, what it does not authorize,
which files implement it, and which tests defend it.

## Feature inventory and acceptance matrix

| Feature | Product sentence | Definition acceptance | Test acceptance | Documentation acceptance |
| --- | --- | --- | --- | --- |
| Archetypes | Validates reusable child-agent template folders before they can seed durable children. | Required files, optional files, ignored files, live-state exclusions, deterministic listing, and validation errors are named. | Covers valid archetype, missing required files, live-state rejection, invalid name/path, examples/profile loading, and stable listing. | Shows valid layout and states that archetypes are templates, not live child state or authority. |
| Bootstrap | Serializes the child's initial remote runtime contract. | Distinguishes bootstrap from live policy, local runtime state, and parent authority. | Covers round trip, normalization, invalid agent/URL/token, missing required fields, and enrollment payload generation. | Explains created -> provisioned -> child starts -> enrolls -> polls policy. |
| Provisioning | Builds the remote child install plan and payload. | Names inputs, output paths, service name, poll interval, binary/bootstrap payload, and dry-run/apply boundary. | Covers dry-run plan, apply payload, unsafe host/user/root/service, missing binary/bootstrap, permission modes, and runner failure. | Explains provisioning prepares a child runtime; it does not activate authority or deploy parent service. |
| Local roots | Computes child-specific workspace and memory roots. | Defines configured roots, default roots, invalid agent handling, and containment expectations. | Covers empty/configured/default roots, invalid IDs, one-root and two-root forms, traversal-like inputs, and root separation. | Documents workspace vs memory ownership and retention expectations. |
| Enrollment | Registers a remote child with the parent control plane. | Defines first enrollment, persisted enrollment state, accepted protocol, tailnet interaction, and rejection modes. | Covers valid enrollment, duplicate/replay, invalid signature, stale timestamp, invalid protocol/agent, missing child, and peer identity constraints. | Documents request/response and that enrollment records identity; it does not widen child authority. |
| Re-attestation | Lets an enrolled child re-prove continuity after control-plane or bootstrap drift. | Defines how it differs from first enrollment and what identity/state may refresh. | Covers valid re-attestation, missing prior enrollment, inactive enrollment, changed peer/secret rejection, stale sequence, and persistence. | Explains re-attestation as continuity evidence, not a new grant. |
| Signed envelopes | Signs and verifies child-parent control messages. | Defines signed fields, canonical payload shape, HMAC scheme, and secret requirements. | Covers valid signature, wrong signature, missing secret/signature, payload mismatch, and constant-time compare path. | Maps envelope fields to integrity, identity, and replay-protection properties. |
| Replay protection | Prevents stale or duplicate control messages from applying silently. | Defines sequence, message ID, timestamp window, receipt replay, and idempotent response semantics. | Covers duplicate message ID, sequence rollback, stale/future timestamp, receipt replay, and duplicate-with-different-payload rejection if applicable. | Explains why accepted requests are durable facts and how old buttons/messages fail safely. |
| HTTP control plane | Exposes parent endpoints for child control-plane traffic. | Defines every route, method, payload, status/error behavior, byte limits, and base-path handling. | Covers route methods, malformed JSON, oversize body, route success/failure, handler base path, and store-backed verifier errors. | Includes route table and links each route to its feature. |
| Tailnet identity binding | Binds accepted control requests to private-network peer identity when required. | Defines required peer fields, hostname/tag checks, first-bind behavior, refresh behavior, and mismatch rejection. | Covers first bind, matching peer, wrong stable node, missing tags, hostname mismatch, stale request no-bind, and identity refresh. | States tailnet reachability is transport identity, not authority. |
| Policy polling | Lets the child fetch the current parent-approved policy snapshot. | Defines known version/hash, changed/no-change response, policy snapshot fields, and store effects. | Covers changed/no-change, invalid signature/envelope, unknown child, stale request, and policy hash/version matching. | Explains child poll -> receive snapshot -> apply locally -> acknowledge. |
| Policy acknowledgement | Records child evidence about received/applied policy. | Defines acknowledged vs applied version/hash, success/failure status, error handling, and state effects. | Covers valid ack, stale/unknown policy, mismatched agent, duplicate ack, failed application status, and store failure. | States ack is evidence of child state; it is not authority creation. |
| Parent conversation polling | Lets a child receive parent-authored messages. | Defines message source, ordering, limits, filtering, and delivery semantics. | Covers none/multiple messages, limit handling, ordering, already-acked filtering, wrong agent, and signed request validation. | Explains parent conversation as bounded parent-to-child guidance, not raw parent prompt mutation. |
| Parent conversation acknowledgement | Lets a child mark parent-authored messages as received. | Defines explicit message IDs, partial ack behavior, duplicate ack behavior, and transactional continuity effects. | Covers valid ack, duplicate ack, partial/unknown IDs, cross-agent rejection, race append preservation, and store failure. | Documents acknowledgement as delivery state only. |
| Review artifact upload | Lets a child upload reports, asks, escalations, and risks for parent review. | Defines artifact fields, accepted kinds/statuses, metadata limits, sensitivity handling, and review event output. | Covers valid upload, invalid/missing artifact fields, sensitive content, oversized content, wrong agent, and rejected store write. | Explains upward flow is artifact-shaped, not raw transcript injection. |
| Review artifact queueing | Converts child artifacts into parent review events and continuity state. | Defines review target requirement, metadata preservation, summary truncation, continuity updates, and source scope. | Covers valid queueing, missing review target, malformed metadata, state identity mismatch, summary clamp, and store errors. | Documents how parent review sees durable child asks/reports. |
| Forensics/redaction | Stores bounded child forensic records and redacts sensitive review summaries. | Defines redaction classes, concrete-secret detection limits, size limits, metadata behavior, and safe operator summary. | Covers obvious secrets, secret-like error metadata, long content, artifact refs, nested metadata, safe summary fallback, and forensic read/write validation. | Documents guarantees and non-guarantees: best-effort screening, not full DLP. |
| Remote HTTP client | Implements child-side HTTP calls to the parent control plane. | Defines signing, sequencing, timeout/context behavior, error decoding, base URL/path joining, and no-policy semantics. | Covers all endpoints, request signing, sequence seeding, HTTP error body decoding, context/timeout behavior where possible, and base-path compatibility. | Identifies the client as protocol adapter, not child policy. |
| Remote runtime sync | Synchronizes child enrollment, policy, parent conversation, and local state. | Defines sync order, enrollment vs re-attestation, policy apply, parent message persistence, and returned result fields. | Covers first enrollment, no-change poll, changed policy, re-attestation, parent messages, failed apply, store errors, and sequence persistence. | Provides lifecycle prose for one sync cycle. |
| Remote child runner | Runs one child-side sync/execute/upload/ack cycle. | Defines execution ordering, input message handling, pending review upload, parent ack, partial failure behavior, and result accounting. | Covers successful cycle, executor error, upload failure, ack failure, no parent messages, pending artifact filtering, and ordering. | Documents one-cycle semantics and that execution authority comes from child policy/executor, not the runner. |
| Remote loop runner | Repeats child-side cycles on an interval. | Defines interval parsing, max iterations, cancellation, error behavior, result aggregation, and sleeper/clock seam. | Covers context cancellation, max iterations, zero/invalid interval, runner error, partial counts, parent-conversation wake loop, and deterministic sleep via seam if needed. | States loop is scheduling wrapper, not autonomy or heartbeat ownership. |
| Snapshots | Captures child workspace/memory and state into restorable records. | Defines snapshot base, manifest, included/excluded files, ID format, permissions, and retention assumptions. | Covers create/list/load, missing roots, empty roots, manifest validation, symlink/path safety, sorting/limit, and state capture. | Documents snapshots as child continuity backups outside child-owned memory. |
| Snapshot restore | Restores a child workspace/memory from a saved snapshot. | Defines overwrite semantics, destructive nature, validation before restore, timestamps, and higher-level approval requirement. | Covers successful restore, missing snapshot, invalid ID/manifest, agent mismatch, partial failure behavior, and traversal safety. | States restore is mutating and requires explicit higher-level approval; it does not authorize activation. |
| Snapshot migration | Migrates older child-memory snapshots into the parent-owned snapshot store. | Defines source, target, duplicate behavior, rejection accounting, and source removal conditions. | Covers valid migration, invalid entries, duplicate/existing destination, mixed results, source removal, and idempotency. | Explains compatibility purpose and failure reporting. |
| Runtime helper | Provides parent-side helpers for durable review artifact handling. | Defines store contract, source scope, state continuity update, metadata encoding, and event creation. | Covers happy path, missing store/target, state mismatch, bad continuity JSON, metadata marshal error, store insert/save errors, and conversation-message projection. | Documents it as adapter for review events, not runtime orchestration owner. |

## Lifecycle summaries

### Remote child first run

1. Parent/admin creates or updates a durable child registry record outside this
   package.
2. Parent writes a remote bootstrap and, when approved, provisions the child.
3. Child reads bootstrap and sends a signed enrollment request.
4. Parent verifies signature, replay window, optional tailnet identity, and child
   registry state.
5. Parent stores enrollment and returns current policy snapshot.
6. Child stores enrollment/policy evidence locally and proceeds only within its
   policy ceiling.

### Routine child sync

1. Child signs a policy poll with current sequence and known policy version/hash.
2. Parent verifies the envelope and returns changed/no-change policy snapshot.
3. Child polls parent conversation and stores any parent-authored messages.
4. Child may run its local executor within its charter and upload bounded review
   artifacts.
5. Child acknowledges received parent messages and applied policy versions.

### Upward reporting

1. Child produces a bounded review artifact.
2. `durableagent/` prepares/redacts/stores forensic context where needed.
3. Parent-side runtime helper converts the artifact into a review event targeted
   at the configured admin review chat.
4. Higher layers decide whether any request becomes a capability grant, policy
   update, memory promotion, or ordinary reply.

### Snapshot lifecycle

1. Parent-approved caller asks to snapshot a child.
2. `durableagent/` copies child workspace and memory plus manifest/state into the
   parent-owned snapshot store.
3. Listing/loading validates generated IDs and manifests.
4. Restore overwrites child workspace/memory from a validated snapshot and must
   remain gated by higher-level approval.

## Package seams

- `core` owns durable-agent data models, normalization, validation, policy
  ceilings, continuity state, and protocol DTOs.
- `session` owns durable storage records and persistence APIs.
- `durableagent` owns substrate operations over those models and storage
  contracts.
- runtime/maintenance layers compose `durableagent` operations into operator
  workflows, alerts, review cards, and commands.
- channel adapters own channel-specific readiness, polling, rendering, and local
  feature behavior.

## Phase-2 coverage notes

Phase 2 hardens the product contract without splitting the package. The added
coverage concentrates on the surfaces that were below 5/5 in the original pass:

- re-attestation now has explicit tests for missing prior enrollment, inactive
  enrollment, and tailnet peer mismatch preservation.
- remote loop scheduling now has isolated deterministic tests for empty-inbox
  parent conversation wakes, ordered JSON inbox processing, partial-result
  failure behavior, and context-cancellation from sleep.
- replay/receipt semantics now include conflict coverage for the same message ID
  with a different accepted envelope.
- parent conversation acknowledgement now has explicit unknown-message rejection
  coverage that proves continuity is not mutated on bad acknowledgements.
- local roots now cover default/session-derived roots and one-root/two-root
  configured forms.
- snapshots now cover nested snapshot exclusion, newest-first/limit listing,
  restore backup behavior, and already-present migration accounting.
- runtime/forensics helpers now cover invalid continuity JSON, cross-agent
  forensic ref rejection, and invalid forensic refs.

These tests are intentionally product-boundary tests: they prove the package
contains child state and transport truth without granting authority, deploying,
restarting, or promoting memory.

## Phase-2 implementation checklist

Phase 2 should only close gaps required by this contract. It should not split the
package by default and should not broaden authority. Expected work:

- add missing tests for re-attestation and remote loop runner
- strengthen local-root, forensics/redaction, parent-conversation ack, policy ack,
  snapshot restore, and migration edge coverage
- add small test seams only where required for deterministic loop/cancellation or
  failure testing
- reconcile docs that still describe implemented remote control-plane behavior as
  future-only
- keep production behavior changes narrow and justified by tests

