# `session/` boundary

`session/` is Aphelion's durable record/store shell. It owns the storage shape
for facts that must survive turns, process restarts, child-agent wakeups, and
operator review. It is intentionally broader than conversational chat history:
SQLite schema, migrations, codecs, record normalization, and atomic persistence
transitions live here.

The shortest boundary sentence is:

> `session/` owns durable facts and atomic persistence transitions, not live
> decisions.

This mirrors the `runtime/` boundary from the other side: `runtime/` owns the
live process shell; `session/` owns the durable records and store mechanics the
shell reads and writes.

## Owned responsibilities

Top-level `session` owns behavior when it is about durable storage, especially:

- record structs and status enums for persisted Aphelion state;
- stable record identity, keys, scope references, and database primary keys;
- SQLite schema creation, migrations, rejection checks, and indexes;
- codecs between Go records, JSON payloads, and database rows;
- normalization of stored records before write or after read;
- store APIs that read, write, list, and search durable records;
- atomic persistence transitions that must happen in one transaction;
- intrinsic record predicates such as `Active`, terminal/expired checks, and
  storage eligibility when those predicates are required to query or persist
  records correctly;
- durable evidence records, including execution, review, progress, memory,
  artifact, tailnet, tool lifecycle, capability, continuation, operation,
  mission, and durable-agent records.

`session` may depend on lower-level record/value packages such as `core` for
shared durable types and accounting fields. Adapter conversion to and from
agent message history belongs here only when it is a storage codec for persisted
messages, not live agent orchestration.

## Non-owned responsibilities

`session` must not become a domain-policy bucket. Code belongs elsewhere when it
owns live behavior, judgment, rendering, or transport semantics. In particular,
`session` must not own:

- runtime orchestration, background loops, process lifecycle, or turn routing;
- turn stage order, governor/face sequencing, or continuation materialization;
- approval UI copy, Telegram button layout, or user-visible rendering;
- policy judgment for whether a capability, tool, child wake, deploy, or
  external-account action is safe to execute;
- live tool execution, credential handling, network calls, account mutation, or
  child-agent wake behavior;
- Telegram command semantics or transport behavior beyond persisted Telegram
  records and offsets;
- Mission Control judgment, mission selection, or mission-rendering behavior;
- doctor/status interpretation beyond durable evidence or cached projections;
- public-contact, purchase, deploy, restart, or permission-grant behavior.

If a function needs `Runtime`, providers, outbound senders, Telegram clients,
tool executors, live credentials, network calls, wake loops, or user-visible
copy, it does not belong in `session`.

## Current subsystem map

The package currently has one Go package and no subpackages. The main clusters
are:

| Cluster | Representative files | Session-owned role | Boundary pressure |
| --- | --- | --- | --- |
| Core session records and chat history | `types_runtime_records.go`, `store_sessions.go`, `store_session_state.go`, `store_session_codec.go`, `store_session_search.go` | Durable session keys, messages, cache metadata, plan/operation/continuation state, search and state lookup | Keep turn sequencing and runtime prompt assembly outside `session` |
| Schema, migrations, and indexes | `store_schema*.go`, `mission_schema.go`, `store_schema_indexes.go`, `store_schema_migrations.go` | SQLite tables, migrations, compatibility checks, indexes | This is the spine; avoid extraction until migration ownership is explicit |
| Continuation, approval, plan, and operation records | `types_continuation.go`, `types_operation.go`, `approval_window_exact_store.go`, `auto_approval_store.go`, `autonomy_override_store.go`, `store_pending_decisions.go`, `store_busy_decisions.go` | Durable proposal, lease, plan, operation, approval-window, and pending-decision records | Approval materialization, gate interpretation, and UI copy belong in `runtime`/helpers |
| Capability, authority, and tool lifecycle | `types_capability.go`, `capability_store.go`, `authority_contract*.go`, `types_tool_lifecycle.go`, `store_tool_*.go`, `store_registered_tools.go` | Persisted capability requests/grants/reviews, authority contracts, registered tool/install/audit/probe records | Live tool safety decisions and invocation authority belong outside `session` |
| Durable-agent persistence | `store_durable_agent*.go`, `durable_child_agreement_store.go` | Durable child identity, policy/bootstrap state, remote metadata, snapshots/agreements | Child wake behavior, channel semantics, and parent review rendering belong outside `session` |
| Mission ledger records | `mission_types.go`, `mission_store.go`, `mission_ledger.go`, `mission_codec.go`, `mission_ask_store.go` | Mission records, events, handoffs, results, evidence, ask prompts, codecs, schema | Mission selection, judgment, and operator-facing render behavior belong outside `session` |
| Telegram persisted records | `store_telegram_*.go`, `types_telegram_thread_promotion.go` | Ingress offsets/updates/failures, thread/message/session mappings, promotion handoff records, reminder storage predicates | Telegram command behavior and UX policy belong outside `session`; reminder policy helpers need care |
| Tailnet records | `tailnet_surfaces.go`, `tailnet_grant_bindings.go`, `store_schema_tailnet.go` | Declared surfaces, grant bindings, statuses, drift/evidence events | Live tailnet control/revocation and network policy decisions belong outside `session` |
| Execution, review, progress, memory, artifacts | `types_runtime_records.go`, `store_execution_events.go`, `store_review_events.go`, `store_progress_views.go`, `store_memory.go`, `store_artifact_retention.go` | Durable evidence, review events, cached progress views, memory/review/artifact-retention records | Rendering, interpretation, and repair decisions belong outside `session` |
| Adapters and model slots | `adapter.go`, `model_slots.go` | Stored message history conversion and durable model-slot override records | Do not grow live model/provider routing here |

## Top-level growth rules

A new top-level `session/*.go` file is acceptable when most of these are true:

1. The code defines persisted records, store methods, schema, migrations,
   indexes, codecs, or transactional state changes.
2. The code can be tested against a store or pure record values without a live
   runtime, provider, Telegram client, network call, or tool executor.
3. The code describes what happened or what is stored, not what the system
   should do next in the live turn.
4. Any policy-like names are intrinsic record facts or storage eligibility
   predicates, not authority to execute live work.
5. The file does not import orchestration or transport packages such as
   `runtime`, `turn`, `pipeline`, or `telegram`.

Prefer a leaf/helper package outside `session` when code is mostly about
classification, rendering, live orchestration, external account/tool behavior,
Telegram UX, mission judgment, child wake behavior, or reusable domain policy.

## Policy-like storage predicates

Some persisted records need lifecycle predicates, cooldowns, statuses, drift
markers, or summaries. Those are allowed in `session` only when they are needed
for durable storage correctness or query behavior.

Allowed examples:

- normalizing a stored status;
- detecting whether a persisted record is active, terminal, expired, revoked,
  stale, or eligible for a store query;
- enforcing an atomic write precondition or compare-and-swap boundary;
- appending durable evidence for a stored lifecycle transition;
- preserving a summary string that another subsystem already produced.

Not allowed examples:

- deciding whether to execute a tool, wake a child, read a mailbox, deploy,
  restart, contact a public surface, or grant authority;
- choosing Telegram button copy, approval prompts, or operator-facing narrative;
- interpreting mission priority or deciding which work should continue;
- deriving live safety policy from stored rows without an explicit runtime or
  authority layer.

When a storage predicate begins to encode product judgment rather than durable
record correctness, queue it for extraction or for a higher-layer helper.

## Extraction criteria

Do not extract code from `session` merely because the package is large. Extract
only when the seam is clearer than the schema coupling.

A subsystem is a good extraction candidate when most of these are true:

- it can expose a small stable record/codecs API back to `session`;
- it does not need direct access to the SQLite transaction or migration spine;
- it has its own domain language that is not generic durable storage;
- moving it will not split schema/migration ownership across packages in a
  confusing way;
- tests can prove behavior equivalence without rewriting broad store fixtures;
- the extraction reduces policy leakage instead of hiding it behind wrappers.

Candidate seams to review later, not move by default:

- mission ledger records and codecs;
- Telegram persistence records and thread/promotion helpers;
- tool/capability lifecycle records;
- tailnet surface/grant records;
- execution/review/progress evidence records.

## Relationship to `runtime/`

`runtime/` reads and writes `session` records while owning live process
coordination. `session/` must stay safe to use from runtime without learning
runtime's live behavior.

Good dependency direction:

```text
runtime/  --->  session/  --->  core/agent value types
```

Forbidden dependency direction:

```text
session/  -X->  runtime/
session/  -X->  turn/
session/  -X->  pipeline/
session/  -X->  telegram/
```

If `runtime` wants a new durable fact, add a record/store shape here. If it
wants a new live action, policy judgment, UI render, or orchestration sequence,
that action belongs outside `session` and should only persist its durable result
through `session`.
