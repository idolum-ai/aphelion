# State Surfaces

![State surfaces](diagrams/05-state-surfaces.svg)

*Session state is split across canonical surfaces, operational
current-state stores, and operator projections; file-backed memory lives
alongside the SQLite ledger.*

Aphelion state is intentionally multi-surface.

## Surfaces

- Visible transcript ledger in `session` (`user`/`assistant` scene text):
  canonical.
- Floor sidecars and floor metadata attached per turn: canonical when recorded
  on messages; `sessions.last_floor_*` is the operational current-state store.
- Plan state and operation state sidecars: operational current-state stores.
- Review events and outbound delivery records: pending review events are
  operational current-state stores; delivered review events and outbound records
  are canonical delivery evidence.
- Turn-run recovery and accounting records for startup repair and live
  diagnosis: operational current-state store.
- Execution event timeline (`execution_events`) for ingress/turn/tool/delivery
  truth: canonical.
- Telegram ingress offset, accepted-update, and poison-update ledgers:
  operational current-state stores for transport recovery.
- Telegram work-surface registry in code for startup replay of primary messages,
  callback work, and decision-resume work: compiled contract.
- Telegram side-thread registry (`telegram_threads`) for per-chat work lanes:
  operational current-state store.
- Typed Telegram decision-resume rows (`pending_busy_decisions` and
  `pending_artifact_retention`) for prompts whose original turn may have been
  interrupted before callback resolution: operational current-state stores.

Code anchors:

- [`session/store.go`](../../session/store.go)
- [`runtime/turn_finalize.go`](../../runtime/turn_finalize.go)
- [`runtime/awareness.go`](../../runtime/awareness.go)
- [`turn/awareness.go`](../../turn/awareness.go)
- [`docs/architecture/transparent-execution-sequence.md`](./transparent-execution-sequence.md)

## Classification Matrix

Classifications below use the shared truth classes defined in
[`docs/architecture/README.md`](./README.md).

| Surface / Store | Classification | Canonical Question |
| --- | --- | --- |
| `session.execution_events` | canonical | What happened in runtime, in what order? |
| `session.messages` | canonical | What scene text was recorded for the session? |
| `messages.floor_content` | canonical | What floor text was captured alongside scene text at message-record time? |
| `messages.floor_metadata` | canonical | What floor metadata/artifact references were captured alongside scene text at message-record time? |
| `session.outbound_messages` | canonical | Which outbound deliveries were recorded at the transport ledger level (not guaranteed human render)? |
| `session.review_events (status='delivered')` | canonical | Which bounded review artifacts were shown to humans? |
| Parent/child memory files and `rhizome_*` tables | canonical | What durable meaning has been retained over time? |
| `session.durable_agents` | canonical | What durable-child identity/config is currently declared? |
| `session.durable_agent_state (identity/config-bearing fields)` | canonical | Which child identity/config handshake facts are currently declared? |
| `session.durable_agent_state (runtime/apply/transient posture fields)` | operational current-state store | What durable-child runtime/apply status is currently declared? |
| `sessions.last_floor_text` | operational current-state store | What floor text is currently declared for the active session? |
| `sessions.last_floor_metadata` | operational current-state store | What floor metadata is currently declared for the active session? |
| `sessions.plan_state_json` | operational current-state store | What plan intent is currently declared? |
| `sessions.operation_state_json` | operational current-state store | What operation intent/stage is currently declared? |
| `pending_decisions` | operational current-state store | What decisions are currently pending and actionable? |
| `pending_busy_decisions` / `pending_artifact_retention` | operational current-state store | Which Telegram decision prompts have typed work that can be resumed after restart? |
| `telegram_ingress_offsets` | operational current-state store | Which Telegram update offset is safe to request next? |
| `telegram_ingress_updates` | operational current-state store | Which Telegram updates are accepted, queued, running, or terminal for transport recovery? |
| Telegram startup work-surface registry | compiled contract | Which typed Telegram ingress surfaces are eligible for startup replay? |
| `telegram_ingress_failures` | canonical | Which Telegram updates failed normalization or handling? |
| `telegram_threads` | operational current-state store | Which per-chat side threads are open or absorbed, and what outcome note closed them? |
| `sessions.continuation_state_json` | operational current-state store | What continuation state, embedded `ActionProposal`, and embedded `ContinuationLease` are currently declared? |
| `mission_ledger` candidate rows projected as pending items | projection | Which durable candidate missions should be visible for operator review now? |
| `session.review_events (status='pending')` | operational current-state store | Which review artifacts are queued for governance delivery? |
| `/status` | projection | How should system/chat state be rendered for operators now? |
| `/health trace` | projection | How should execution evidence be rendered for diagnosis now? |
| `provider_health` in `/health` | projection | Is recent inference-provider pressure the current explanation for slow, failed, or retried work? |
| Quick-read and progress render blocks | projection | What compact operator narration should be surfaced now? |
| `turn_runs` | operational current-state store | What startup recovery/run bookkeeping and accounting hints are available for interrupted or active work? |
| `curiosity_leases` | operational current-state store | What config-backed read-only curiosity allowance is active, expired, or exhausted for the current period? |
| `curiosity_observations` | canonical | What silent read-only observations were actually recorded from candidate-bound curiosity looks? |

## Removed Surface Rule

Historical rows and aliases that are no longer part of the current truth model
must be deleted or rejected. They must not become operator projection inputs.
- When `/status` or `/health trace` uses fallback rows, that usage should be surfaced
  as source attribution.

ActionProposal / ContinuationLease note:

- In v1 these records are embedded in `sessions.continuation_state_json` so the existing continuation button flow remains the operational current-state surface.
- TES `continuation.*` events remain canonical for what was offered, approved, consumed, revoked, or blocked at runtime.

Turn-run accounting note:

- `turn_runs` now also stores operational counters such as turn index, tool input
  characters, assistant output characters, provider input/output tokens, and
  provider cache read/write tokens.
- Those counters support `/status`, `/health trace`, and doctor diagnosis. They
  do not replace TES as execution-order truth and should not be treated as a
  canonical transcript.

Curiosity note:

- `curiosity_leases` are operational allowance records, not operator approval
  leases and not capability grants.
- `curiosity_observations` are typed facts produced by read-only candidate-bound
  looks. They may feed interior signal pressure, but they do not directly write
  curated memory, assert completion, or create authority.

Staged identity decision:

- `session.durable_agents` is canonical for durable child identity/config.
- `session.durable_agent_state` is split by meaning:
  - identity/config-bearing fields are canonical identity/config.
  - runtime/apply/transient posture fields remain operational current-state.

## Why This Matters

- Keeps user-visible continuity and machine-audit continuity separate.
- Preserves floor/scene split without losing recovery/review semantics.
- Prevents architecture drift into one hidden “memory blob.”
- Makes `/status`, `/health trace`, and progress narration converge on one shared execution timeline instead of independent ad-hoc state machines.
- Keeps transport poison messages explicit: a skipped Telegram update is a
  ledgered fact with update ID, surface, refs, and error text, not an invisible
  in-memory offset jump.
- Keeps offset advancement tied to accepted outcomes: a Telegram update past the
  saved offset is either terminally recorded or recoverably queued for replay.
- Keeps replay explicit: primary messages, callback work, and decision-resume
  work are declared as typed startup work surfaces instead of rediscovered from
  command text.
- Keeps redelivery idempotent: a terminal Telegram ingress row is not dispatched
  again, and accepted work is dispatched only while its ledger status remains
  accepted or queued.

Related requirements:

- [`requirements/sessions.md`](../../requirements/sessions.md)
- [`requirements/operations.md`](../../requirements/operations.md)
- [`requirements/hidden-inputs.md`](../../requirements/hidden-inputs.md)
- [`requirements/reliability.md`](../../requirements/reliability.md)
