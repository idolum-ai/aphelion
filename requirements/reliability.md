# Reliability — Error Handling, Recovery, Degradation, and Disaster Discipline

## Overview

Aphelion is not a stateless chatbot. It is a long-running governed system with:

- durable session state
- background maintenance loops
- tool execution
- external transports
- external model providers
- credential dependencies

That means failure handling must be explicit.

This spec defines how Aphelion should behave when things go wrong:

- what counts as a normal operational failure
- what should be retried
- what should be degraded
- what must be surfaced
- what must be journaled durably
- what should trigger maintenance recovery

The design goal is not "never fail."

The design goal is:

- fail honestly
- preserve continuity
- avoid silent corruption
- degrade to a simpler truthful mode
- leave enough machine-authored evidence for Aphelion to recover coherently

## Telos

This spec follows Aphelion's broader telos:

- **constitutional core over theatrical confidence**
- **local durable truth over ambient hidden state**
- **editable soul over non-editable floor**
- **governor authority over face appearance**
- **machine facts first, governor interpretation second**

Reliability therefore depends on the system being explicitly aware of its own degraded or recovered state. See `self-awareness.md`.

In reliability terms, that means:

- runtime should record what actually happened before asking Aphelion to interpret it
- `Idolum` may soften or explain failures, but may not hide them
- fallback should preserve the authority model
- disaster recovery should restore coherence, not just restart processes
- degraded runtime truth should be injected, not left implicit

## Reliability Principles

### 1. Failure is a first-class runtime outcome

The system must treat these as normal operational states:

- provider unavailable
- face renderer unavailable
- tool execution failure
- tool timeout
- sandbox denial
- Telegram send/edit/delete failure
- startup interruption
- config invalid
- auth unavailable
- heartbeat/cron failure

These are not exceptional in the architectural sense. They are expected modes of operation.

### 2. Facts before interpretation

Every important failure should first become a machine-authored fact:

- turn run status
- provider status code
- last tool name
- progress message id
- error text
- retry count
- delivery status

Only after that should Aphelion generate a maintenance or recovery interpretation.

Those machine-authored facts should also be available to the governor as part of runtime self-awareness when they affect the current turn.

### 3. Degrade before aborting the whole system

If a component fails, the system should prefer a narrower truthful mode over total collapse.

Examples:

- face failure -> floor-to-user fallback serializer
- streamed reply failure -> non-streamed reply
- voice synthesis failure -> text reply
- Codex unavailable in `auto` mode -> native governor
- native Anthropic unavailable after bounded retries -> OpenRouter when configured
- reflection failure -> skip reflection, continue heartbeat
- malformed brokerage ratification -> plain proposal fallback

### 4. Never silently pretend success

If the system falls back, the runtime must know it fell back.

That means:

- logs must distinguish normal path from degraded path
- turn metadata should eventually distinguish canonical path from fallback path
- maintenance recovery should be able to reason over fallback history
- the live governor should know when it is on a degraded path if that affects decision quality or user honesty

### 5. Protect the ledger

When forced to choose between:

- sending a reply
- preserving coherent state

the system should prefer preserving coherent state unless the outbound message can be durably reconciled afterward.

The visible ledger, floor sidecar, turn runs, and outbound records form the continuity backbone.

### 6. Recovery is a maintenance turn

Recovery should not be hidden imperative glue alone.

The runtime should:

1. record machine facts
2. mark interrupted work
3. let Aphelion analyze the interruption in a maintenance session

This preserves the "living operator" model of the system.

## Failure Taxonomy

Failures are grouped into six classes.

### A. Configuration and bootstrap failures

Examples:

- invalid TOML
- missing admin principal
- mismatched backend config
- missing required secrets for enabled features
- invalid filesystem roots

Required behavior:

- fail fast at startup
- return actionable error messages
- prevent restart loops where possible
- provide a `--check-config` path

### B. Runtime turn failures

Examples:

- provider transport error
- provider 429/500/503
- tool registry missing
- tool timeout
- tool non-zero exit
- compaction summarization failure

Required behavior:

- keep the session coherent
- complete turn runs with explicit status
- degrade where safe
- return bounded user-visible failure text when no truthful success path remains
- point the user to `/stop` when backend failure may leave current work in flight

### C. Delivery failures

Examples:

- Telegram send failure
- Telegram edit failure
- Telegram delete failure
- progress-message failure

Required behavior:

- record the attempted outbound action
- preserve enough state to reconcile later
- do not assume the user saw a message that was never acknowledged
- keep progress artifacts bounded and readable enough that degraded-mode guidance remains legible

### D. Background loop failures

Examples:

- heartbeat error
- cron job error
- idle expiry sweep error
- reflection error

Required behavior:

- isolate the failed iteration
- keep the loop alive
- log the failure
- optionally produce maintenance evidence

### User-facing hard-failure guidance

When the system exhausts retries and fallback for a live user turn:

- the user should receive a bounded truthful failure message
- the message should say that the turn did not complete
- the message should point to `/stop` as the safe chat-level escape hatch

Example shape:

- "Inference backends are unavailable after retries and fallback. This turn did not complete. You can /stop to cancel current work and try again."

The fallback serializer should remain deterministic in current implementation.

It may be channel-aware and house-shaped, but it should not become a second model-authored scene path under degradation.

The runtime should not tell ordinary users to kill or restart the whole service from chat.

Future admin-only operational controls such as `/restart` may exist, but they are distinct from end-user turn cancellation.

### E. Recovery and restart failures

Examples:

- process restart during active turn
- recovery analysis failure
- orphaned progress message
- interrupted tool execution

Required behavior:

- mark interrupted runs
- keep machine facts
- run startup recovery opportunistically
- never claim completion of interrupted work
- after successful startup recovery, send the admin a concise operator-facing restart catch-up message when interrupted runs existed

The operator-facing restart catch-up message should:

- summarize the interruption in plain speech rather than replaying raw recovery ledger text
- include the most recent interrupted request when available
- include the last tool in flight when available
- include a sanitized recovery note if one exists
- point to the next priority or safest next move
- avoid leaking maintenance-only scaffolding such as fenced ledger blocks, section tags, or raw `run_id=` prefixes unless no cleaner summary is available

### F. Disaster scenarios

Examples:

- DB corruption
- DB missing unexpectedly
- prompt root unreadable
- credentials malformed
- service restart storm
- provider auth revoked

Required behavior:

- prefer safe mode over undefined behavior
- provide operator-visible remediation path
- avoid destructive automatic self-repair unless explicitly scoped and reversible

## Reliability Surfaces

### Session ledger

The session ledger must remain the source of conversational continuity.

Requirements:

- visible scenes stored as visible transcript
- governor floor stored as sidecar audit data
- compacted turns preserved on disk for audit
- background and maintenance sessions remain first-class ledgers

### Turn-run journal

Turn runs are the machine-authored execution ledger.

They must record:

- kind
- status
- request text
- start and completion timestamps
- last activity timestamp
- last tool facts
- progress message id
- machine error text
- recovery summary and recovery logged timestamp
- whether provider failover occurred
- which provider/backend finally succeeded, if any

Turn runs are required for:

- startup interruption handling
- operator debugging
- future delivery reconciliation

### Outbound ledger

Every outbound user-visible send should be durably linked to:

- session key
- turn index
- transport message id
- delivery type

The system should evolve toward explicit delivery state:

- `pending`
- `sent`
- `acked`
- `failed`
- `abandoned`

### Maintenance ledger

Maintenance work belongs in a first-class maintenance session, not hidden logs alone.

That includes:

- startup recovery notes
- reflection notes
- heartbeat conclusions
- later delivery-reconciliation notes

## Fallback Ladders

Fallback must be explicit and ordered.

### Governor backend ladder

#### `backend = "auto"`

1. try Codex
2. if Codex auth unavailable, use native governor
3. if Codex request gets `401`, refresh/reload and retry once
4. if still unavailable, degrade to native governor
5. if no native governor is configured, fail startup

#### `backend = "codex"`

1. require usable Codex auth
2. attempt refresh/reload on `401`
3. if a native provider chain is configured for runtime failover, degrade to that chain
4. otherwise fail the turn or startup according to where the failure happened

#### `backend = "native"`

1. use configured native provider
2. apply provider retry policy
3. if configured, fail over across the native provider chain in order
4. if unavailable after retries and fallback, return bounded provider-failure output for the turn

### Native provider ladder

The native provider chain should be explicit and ordered.

Example:

1. Anthropic
2. OpenRouter

Rules:

- retry within one provider first
- fail over only on retryable exhaustion or retryable transport failure
- do not cascade through every provider on deterministic request/config/auth errors
- successful failover must be visible to logs and machine state even if the user-visible turn completes normally

### Face ladder

1. if brokerage policy says brokerage is useful, try brokerage proposal first
2. if brokerage proposal fails, rerun the ordinary proposal path when proposal policy allows it
3. if brokerage ratification fails after a brokerage note exists, rerun a true plain proposal instead of relabeling brokerage text as proposal
4. if that proposal rerun fails too, preserve the brokerage advisory honestly or continue without a face proposal
5. if no brokerage path is active and proposal policy says proposal is useful, try proposal directly
6. if plain proposal fails, continue without proposal
7. try Idolum render when policy says render is useful
8. if stream render fails, fall back to non-stream render
9. if non-stream render fails, bypass scene authorship and invoke the dedicated floor-to-user fallback serializer for this turn
10. if face backend is `floor_fallback`, skip face scene authorship and invoke the fallback serializer directly
11. if fallback serialization fails too, direct raw floor delivery may be used as an emergency last resort and must be recorded as such

The face must never block a valid governor reply from being delivered.

### Voice ladder

For inbound voice:

1. attempt transcription
2. if transcription fails, fail the turn truthfully

For outbound voice:

1. when mode requires voice, attempt ordinary scene authorship first
2. if scene authorship is unavailable or skipped, use the deterministic spoken fallback serializer
3. attempt voice synthesis on the chosen spoken text
4. if synthesis fails, send text fallback
5. record whether the delivered artifact was degraded from scene to spoken fallback, or from voice to text

For inbound image/document turns:

1. resolve admission before any media download
2. refuse oversized supported media before expensive processing when possible
3. if a supported image turn cannot reach a vision-capable native provider, fail honestly rather than pretending the image was read
4. if PDF extraction fails, surface that as a bounded failure or bounded placeholder rather than inventing document contents

### Tool ladder

For tool execution:

1. resolve scope
2. resolve backend
3. execute under timeout/resource limits
4. convert tool failure into tool result or explicit turn failure depending on where it occurred

Tool failure does not automatically fail the turn if the governor can still answer coherently after seeing the tool error.

### Compaction ladder

1. estimate active prompt size
2. if below threshold, do nothing
3. if above threshold and strategy is `summarize`, try summary compaction
4. if summarization fails, fall back to `truncate`
5. if even truncation cannot achieve a safe prompt window, fail the turn with explicit context-limit error

## Retry Policy

Retries must be narrow and policy-driven.

### Provider retries

Allowed by default:

- 429
- 500
- 503
- transient transport failure

Default properties:

- bounded retry count
- exponential backoff
- context cancellation honored

Not retried blindly:

- malformed request
- auth unavailable without a refresh path
- deterministic validation errors

### Telegram retries

Transport retries should be more conservative than provider retries.

Required principles:

- never duplicate a user-visible message without reconciliation logic
- edits may be retried if idempotent
- sends should be retried only when the delivery semantics are understood

In current implementation, Telegram send failures may simply fail the turn after state persistence.
In future phase, the system should grow explicit pending/failed outbound reconciliation.

### Maintenance loop retries

Heartbeat and cron should not recurse on failure inside the same iteration.

The retry unit is the next scheduled iteration, not an immediate loop-within-loop retry.

## Delivery Semantics

The system must distinguish:

- reply generation
- reply persistence
- reply transport
- reply acknowledgement

### Interactive turns

Preferred order:

1. run governor
2. choose face path
3. persist session results
4. send outbound
5. persist outbound record

This is already close to the current architecture, but the long-term spec should add explicit outbound state transitions.

### Streamed replies

Streamed replies are provisional until the final message state is known.

The system should treat:

- initial streamed send
- intermediate edits
- final visible text

as a delivery envelope, not just a convenience transport.

If the stream fails mid-turn:

- the system should either finalize a truthful truncated reply or restart delivery via a non-streamed send
- the ledger should know that a streaming attempt occurred

### Background deliveries

Heartbeat and cron deliveries must obey the same ledger discipline as ordinary turns.

They should not bypass audit simply because they are background-generated.

## Recovery Model

Recovery has three phases.

### Phase 1. Detection

On startup:

- mark any still-running turn runs as `interrupted`
- capture machine-authored interruption facts
- gather unrecovered interrupted runs

### Phase 2. Analysis

Run a maintenance recovery turn that:

- sees the interrupted run facts
- analyzes likely stopping points
- suggests safe next actions
- writes a recovery note into the maintenance ledger

### Phase 3. Optional surface

Recovery notes should remain maintenance-local by default.

User-facing or admin-facing delivery should be a policy decision, not an automatic consequence of every interruption.

## Disaster Recovery

Disaster recovery is broader than startup interruption.

### SQLite problems

Required minimum behavior:

- fail fast on open/init failure
- never auto-delete a corrupted DB
- provide an operator-visible path to rotate or restore the DB

Later goals:

- lightweight integrity check command
- optional backup/rotate command
- startup safe mode when the DB is unavailable

### Config disasters

Required behavior:

- exit with dedicated config error code
- avoid restart loops in systemd for invalid config
- include actionable remediation text

### Auth disasters

Required behavior:

- Codex auth failures are explicit
- refresh-token failures do not silently mutate backend semantics
- secrets are redacted in transport errors

### Filesystem disasters

Required behavior:

- fail clearly when prompt roots or state roots are unreadable
- do not rebuild missing curated memory from imagination
- `init` may seed missing defaults, but only in explicitly owned roots

## Operator Surfaces

Reliability is not only runtime behavior. It also needs operator inspection surfaces.

### Required surfaces

- `--check-config`
- `paths`
- `gc`
- `forget`
- `reset`
- systemd-friendly exit codes
- structured startup logs

### Recommended next surfaces

- `aphelion status`
  - provider backend
  - face backend
  - active loops
  - pending interrupted runs
  - recent degraded mode flags

- `aphelion doctor`
  - config validation
  - prompt root readability
  - DB connectivity
  - governor auth availability
  - transport auth sanity

- `aphelion recover`
  - explicit operator-triggered recovery turn
  - optional dry-run

## Observability

The system should log with enough structure to answer:

- what failed
- in which turn kind
- for which session
- after how many retries
- under which backend
- whether fallback occurred

The spec target is eventually structured logging, but plain logs are acceptable at current implementation if the fields are consistent.

Recommended common fields:

- `chat_id`
- `user_id`
- `turn_run_id`
- `kind`
- `provider`
- `governor_backend`
- `face_backend`
- `fallback`
- `retry_count`
- `status_code`

## Configuration Surface

This spec implies a reliability-oriented config surface such as:

```toml
[reliability]
provider_max_retries = 3
provider_initial_backoff = "100ms"
stream_delivery_mode = "best_effort"   # "best_effort" | "strict"
recovery_on_startup = true
recovery_max_runs = 20

[reliability.face]
proposal_policy = "adaptive"           # "adaptive" | "always" | "never"
render_policy = "adaptive"             # "adaptive" | "always" | "never"

[reliability.delivery]
record_pending_outbound = true
reconcile_failed_outbound = false
```

Not all of this must be implemented immediately. But the spec should reserve the concepts.

## Test Plan

### Startup and config

- **TestInvalidConfigFailsFast**
- **TestConfigExitCodeIs78**
- **TestInvalidConfigPreventsRestartLoop**
- **TestPrepareFilesystemFailure**

### Governor/provider resilience

- **TestProviderRetry429**
- **TestProviderRetry500**
- **TestProviderRetry503**
- **TestProviderFailureReturnsBoundedReply**
- **TestCodex401RefreshRetry**
- **TestAutoGovernorFallsBackToNative**
- **TestExplicitCodexDoesNotSilentlySwitchToNative**

### Face resilience

- **TestProposalFailureDoesNotFailTurn**
- **TestFaceStreamFailureFallsBackToNonStream**
- **TestFaceRenderFailureFallsBackToFloor**
- **TestAdaptiveFaceSkipsMechanicalTurns**

### Tool resilience

- **TestToolTimeoutBecomesToolError**
- **TestToolSandboxDenialIsExplicit**
- **TestToolFailureDoesNotCorruptLedger**

### Session and compaction

- **TestCompactionTriggerByContextEstimate**
- **TestCompactionSummarizeFallbackToTruncate**
- **TestCompactionPreservesAuditRows**
- **TestCompactionFailureReturnsExplicitContextError**

### Delivery resilience

- **TestOutboundSendFailureAfterPersistence**
- **TestStreamEditFailureDegradesGracefully**
- **TestHeartbeatDeliveryFailureLeavesMaintenanceLedgerIntact**
- **TestCronDeliveryFailureLeavesCronSessionIntact**

### Recovery

- **TestInterruptRunningTurnRunsOnStartup**
- **TestStartupRecoveryWritesMaintenanceNote**
- **TestStartupRecoverySendsAdminCatchupMessage**
- **TestStartupRecoveryCatchupSanitizesLedgerScaffolding**
- **TestRecoveryDoesNotClaimCompletedWork**
- **TestRecoveryFactThenInterpretationOrder**

### Disaster handling

- **TestDBOpenFailureFailsStartup**
- **TestCorruptDBDoesNotAutoDelete**
- **TestUnreadablePromptRootFailsClearly**
- **TestMissingCodexAuthInExplicitModeFailsClearly**

## Staging

### Current Phase

- keep current retry and fallback behaviors explicit in spec
- formalize startup recovery and bounded fallbacks
- add delivery and fallback terminology
- keep disaster handling mostly operator-driven

### Next Phase

- outbound reconciliation state
- explicit status/health-diagnosis surfaces
- broader loop/degraded-mode metrics
- stronger distinction between retryable and non-retryable transport errors

### later

- backup/restore tooling
- DB integrity tooling
- structured log/event export
- richer Telegram and CLI operator projections

## Decisions

- **Recovery is not magic.** It is a governed maintenance function built on machine facts.
- **Fallback is better than collapse, but worse than the primary path.** It must stay visible to the runtime.
- **Idolum may soften failure language, but may not conceal system reality.**
- **The ledger outranks appearance.**
- **Transport success and turn success are related but not identical.**
- **Interruptions should become durable facts, not just logs.**
- **Disaster recovery should favor honesty and reversibility over aggressive self-healing.**
