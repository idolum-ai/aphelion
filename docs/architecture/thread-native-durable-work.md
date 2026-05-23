# Thread-Native Durable Work

_Status: exploratory architecture direction._

This note describes a possible simplification of Aphelion's durable child model:
make the thread the operator-facing unit of durable work, and treat child-agent
machinery as backing implementation only when a thread needs isolation, wake,
transport binding, or remote execution.

The goal is not to remove governance. The goal is to preserve the speed and
natural feel of `/thread` while letting the same lane progressively gain the
durable features that currently require creating and operating a separate
durable agent.

North-star shape:

> A thread is the durable unit of work; authority, wake, memory, transport, and
> isolation are capabilities attached to that thread.

## Why Consider This

Side threads have the right operator texture:

- Starting one is cheap: `/thread <message>`.
- Continuing one is obvious: reply to its messages or use `(thread N)`.
- Closing one is cheap: `/absorb N`.
- The lane remains inside the normal Telegram radio link, busy gate, turn
  router, progress state, continuation approvals, memory focus, and replay
  recovery.

Durable children have the right governance ingredients, but the operator path
is heavier:

- The user has to create and name a separate entity.
- The role, charter, bootstrap, policy, wake mode, review target, storage roots,
  and channel binding are all exposed early.
- The surface area splits between `/thread`, `/agents`, durable-agent CLI,
  child wake state, review artifacts, Tailnet control, and capability tools.
- Simple work feels brittle because the system asks the operator to manage the
  substrate before the work lane has proved it deserves that substrate.

The design pressure is clear: keep thread creation lightweight, then add durable
material only at the moment the thread actually needs it.

## Operator Surface Split

The simplest operator split is:

- `/threads`: lightweight scratch and side-work lanes.
- `/agents`: promoted durable lanes with wake, policy, access, binding, review,
  and lifecycle controls.

In this model, `/threads` should not grow a full durable control deck. It should
show the actions that belong to ordinary side threads:

```text
Thread 2: Inbox triage plan

[Continue] [Promote] [Absorb]
```

`Promote` is the explicit boundary crossing. It says this lane has become
important enough to retain, govern, wake, or bind. Promotion should preserve the
thread's useful context while creating the durable records needed for later
authority and operation.

```text
Promote Thread 2?

This keeps the lane as a durable agent with its current context.
Default policy: draft-only, ask before external action.

[Promote] [Edit Policy] [Cancel]
```

After promotion, `/agents` becomes the richer durable work board:

```text
Agents

Inbox Triage
from thread 2, draft-only, no wake, no external access

[Continue] [Wake] [Access] [Policy] [Archive]
```

This preserves the snappy thread experience and avoids asking every side thread
to carry durable controls. It also keeps a clear operator moment where the
system can explain that durable work has stronger lifecycle and authority
semantics.

The danger is merely moving today's friction behind a `Promote` button. To avoid
that, `/agents` must be thread-like and card-driven. It should hide agent IDs,
bootstrap ceilings, channel kinds, storage roots, and CLI-shaped setup details
unless the operator opens details or diagnostics.

Promotion should be continuity-preserving, not a copy-and-forget migration:

- the promoted agent keeps a short human label derived from the thread;
- the source thread keeps a backlink to the promoted agent;
- `Continue` on the agent should feel like continuing the original lane;
- the thread transcript should remain available as bounded context or evidence;
- raw backing principal IDs should stay in details, health trace, and
  maintenance surfaces.

## Promotion Wizard / Contract Solidification

`Promote` should open a wizard, not silently transform a side thread into an
agent. A normal thread may have been shaped by parent context, broad memory
focus, active tools, operator habits, and ad hoc workspace access. Promotion is
the point where those ambient conditions are made reviewable and compiled into a
child-owned contract.

The wizard has two jobs:

1. preserve the useful continuity that made the thread worth promoting;
2. prevent parent memory, credentials, tools, network, filesystem scope, or
   authority from leaking into the promoted agent by resemblance.

A concrete example:

```text
Thread 8: Aphelion branch review

This thread inspected origin/thread-native-durable-doc in an isolated worktree,
merged origin/main locally without committing, read the added architecture doc,
and produced a grounded review.

Promote as: Aphelion Worktree Scout
Default posture: local drafts, ask before external effects

[Review Context] [Review Access] [Create Agent] [Cancel]
```

The source thread can feel continuous to the operator, but the promoted agent
must receive only an approved handoff:

- a charter and first task;
- a selected memory/context digest;
- explicit filesystem, command, tool, and network grants;
- policy and stop conditions;
- an artifact index and source-thread backlink;
- a promotion handoff artifact for recovery, `/status`, and health trace.

That handoff artifact is important. Recovery should not infer what was promoted
from chat prose after a restart. It should be able to read a typed record that
says which context was transferred, which resources were granted, which
boundaries were rejected, and what the first supervised task was.

### Wizard Steps

A practical wizard can be staged as follows.

1. **Candidate and purpose.** Confirm the source thread, human label, charter,
   parent scope, proposed autonomy, and first task. Nothing durable is granted
   by this screen alone.
2. **Context and memory exposure review.** Show the operator a summary of the
   thread transcript, active plan/operation state, files read, commands run,
   artifacts created, approvals used, and parent-memory snippets that materially
   shaped the work. Offer a proposed child memory digest with include/exclude
   controls. The default is distilled memory candidates, not raw core-memory
   inheritance or transcript dump.
3. **Resource, command, and network review.** Convert the thread's working
   conditions into explicit proposed grants. The operator should see both what
   the thread used and what the child will actually receive.
4. **Policy compilation.** Compile the selected charter, autonomy, outbound
   mode, visibility, drift policy, stop rules, approval gates, and capability
   grants into typed records.
5. **First supervised handoff.** Start the child with a narrow orientation task:
   re-read its assigned state, report what it believes it can do, and stop for
   parent review before expanding autonomy.
6. **Promotion receipt.** Store a promotion handoff/result artifact containing
   the final selected context digest, grant set, denied grants, first task,
   source refs, and expected validation evidence.

For the Aphelion worktree example, the resource screen might compile to:

```text
Filesystem
- allow read/write: workspace/worktrees/aphelion-thread-native-durable-doc
- allow read-only comparison: code/github.com/idolum-ai/aphelion
- deny: secrets, unrelated home directories, active checkout mutation

Commands
- allow: git status, git diff, git log, git show, grep/search, file reads
- allow with review: go test ./... inside assigned worktree
- gated: file edits outside assigned doc/task scope
- deny without new approval: commit, push, deploy, restart, install deps

Network
- allow: mediated GitHub fetch for idolum-ai/aphelion refs
- deny by default: arbitrary public web, credential export, external contact
```

The child can carry forward the thread's habits, but only as explicit contract:
inspect before editing, keep a current plan, cite file paths and commands,
validate meaningful changes, report uncertainty, and escalate gated actions.
The wizard turns those habits into policy, not ambient permission.

## Absorb Review Threads

Promotion needs a symmetric return path. If `/threads` can promote a lane into
`/agents`, `/agents` should be able to absorb a promoted agent back into an
ordinary conversation lane.

The source thread may no longer exist, may already be absorbed, or may no longer
be the right place for review. The simpler rule is:

> Absorbing a promoted agent creates a new absorb review thread using the next
> available lowest visible thread number.

That thread is not a continuation of the child's work. It is a parent-facing
review session whose job is to decide what, if anything, should be incorporated
into the parent.

```text
Thread 1: Absorb Inbox Triage

I am reviewing Inbox Triage for possible incorporation.

Available roll-up material:
- outcome summary
- memory candidates
- recurring workflow candidates
- active grants
- artifacts
- open questions
- risk notes

What should we keep, forget, revoke, or fold into parent memory?
```

This gives the parent persona a bounded semantic session for judgment without
silently merging child context into parent memory, policy, or authority. The
conversation can use natural language:

```text
Keep the receipts workflow and the lesson about sender heuristics,
but revoke mail access and do not keep the daily wake.
```

The runtime should compile the conversation into a typed absorb plan:

```text
Absorb plan for Inbox Triage

Roll up:
- outcome summary to Thread 1
- 2 memory candidates for review
- artifact index with 4 reports
- open question about receipts

Teardown:
- revoke read-only mail grant
- cancel daily wake
- archive child as absorbed

[Apply] [Edit] [Cancel] [Details]
```

The absorb review thread can then be absorbed into the main chat like any other
side thread after the plan is applied or canceled. The key invariant remains:
review text is presentation and deliberation; incorporation happens only through
typed memory candidates, capability disposition records, wake teardown records,
artifact indexes, and child lifecycle state.

## Proposed Vocabulary

- **Thread**: an operator-visible work lane with a durable session scope, queue,
  transcript, progress state, approvals, and lifecycle.
- **Durable thread**: a thread with one or more durable attachments such as a
  charter, schedule, capability grant, local workspace, or external binding.
- **Thread profile**: typed metadata for the thread's role: title, charter,
  memory focus, model preference, and parent scope.
- **Thread policy**: typed authority limits for the thread: outbound mode,
  autonomy, drift policy, visibility, capability envelope, and stop conditions.
- **Thread wake**: a typed trigger that can enqueue work for the thread: schedule,
  parent prompt, external adapter poll, push signal, or deploy/recovery wake.
- **Thread binding**: a compiled transport or runtime binding such as a Telegram
  group, local external adapter, Tailnet remote, or isolated child process.
- **Backing principal**: an internal principal used when a thread needs separate
  capability grants, storage, sandbox identity, or remote enrollment.
- **Promoted agent**: the operator-facing `/agents` card created from a thread
  when durable controls become useful. Internally it may be backed by a durable
  thread profile, a durable-agent record, or another backing principal.
- **Absorb review thread**: a fresh side thread created when a promoted agent is
  being reintegrated. It gives the parent persona a bounded conversation for
  choosing memory, policy, artifact, wake, grant, and teardown outcomes.

The operator should mostly see threads. Backing principals and legacy durable
agent IDs can remain visible in details, health traces, CLI maintenance, and
forensic records.

## Operator Flows

The basic flow stays unchanged:

```text
/thread investigate inbox triage
(thread 1) draft a read-only plan
/threads
/absorb 1
```

Durability becomes progressive:

```text
(thread 1) keep this as Inbox Triage
(thread 1) wake every morning with a summary of new mail
(thread 1) request read-only mail access
(thread 1) ask me before deleting anything
```

The runtime should translate those requests into typed records:

- a thread profile for "Inbox Triage";
- a wake schedule for the morning review;
- a capability request and grant for read-only mail access;
- a thread policy that forbids destructive mail operations without approval.

If the command comes from an unpromoted side thread, the first response can be a
promotion proposal instead of immediately exposing all durable controls:

```text
Thread 1 needs durable controls for a morning wake.

[Promote And Configure Wake] [Promote Only] [Cancel]
```

External or remote work keeps the same operator shape:

```text
(thread 2) bind this to the family group as a read-only helper
(thread 3) run this on the Tailnet host aphelion-lab
```

Those requests may still materialize specialized runtime records underneath, but
the first-class operator object remains the thread.

After promotion, the operator can manage durable features from `/agents` without
memorizing specific command grammar:

```text
Inbox Triage Wake

Schedule: every morning at 8:00
Scope: Inbox Triage only
Last run: completed today 8:02
Next run: tomorrow 8:00

[Change Time] [Pause] [Run Now] [Details]
```

Buttons are projections of typed state. They should not be treated as command
shortcuts that bypass parsing or authority checks. Callback payloads should carry
canonical IDs, and every action should re-read current state before mutating it.

When the work should return to parent context, `/agents` should offer `Absorb`:

```text
Inbox Triage

[Continue] [Wake] [Access] [Policy] [Absorb]
```

`Absorb` creates a fresh absorb review thread. The parent can discuss the child
context there, then apply a typed absorb plan. This is the path for semantic
incorporation into parent-facing sessions without dumping a child transcript
into the main chat.

## Attachment Model

A durable thread can be represented as a small base lane plus optional typed
attachments:

| Attachment | Purpose | Truth class |
| --- | --- | --- |
| Thread row | Open/closed state, display slot, created text, absorb summary | operational current-state store |
| Thread session | Transcript, floor sidecars, plan state, operation state | canonical or operational per field |
| Thread profile | Durable role, title, charter, memory focus, model preference | canonical |
| Thread policy | Autonomy, outbound mode, drift policy, visibility, capability envelope | canonical |
| Thread wake | Schedule, queue, retry/backoff, last attempt/result | operational current-state store |
| Thread binding | Telegram group, adapter, isolated process, Tailnet remote | canonical for declared binding |
| Capability records | Requests, reviews, grants, invocations | canonical |
| Promotion handoff | Selected context digest, grant set, denied grants, first task, source refs, expected validation | canonical after approval |
| Execution events | Runtime evidence for wake, delivery, tool use, failure, recovery | canonical |
| Absorb plan | Roll-up choices, memory candidates, grant disposition, wake teardown, archive action | canonical after approval |

The important constraint is that text remains presentation. A message like
"wake every morning" proposes a durable change; it does not become authority
until compiled into a schedule, lease, grant, or other typed record.

## Mapping To Current Code

Current `/thread` already has most of the lane mechanics:

- `session/store_telegram_threads.go` defines `TelegramThread`,
  `CreateTelegramThreadForUpdate`, `ListTelegramThreadsByView`,
  `TouchTelegramThread`, `CloseTelegramThread`, `RecordTelegramThreadAbsorb`,
  and reply-message lookup. The row is already per-chat, idempotent by source
  update, and atomic when absorb writes the main-chat note.
- `session/store_schema.go` creates `telegram_threads`,
  `telegram_callback_messages`, and the thread-session backfill migrations.
- `session/scope.go` and `session/types.go` define
  `ScopeKindTelegramThread`, `TelegramThreadScopeRef`, and
  `SessionIDForKey`, giving each side thread a durable `telegram_thread:*`
  session lane.
- `internal/telegramcommands/commands_threads.go`,
  `commands_threads_view.go`, and `commands_callback_router.go` provide
  `/thread`, `/threads`, `(thread N)` prefix routing, reply routing, summarize
  callbacks, absorb callbacks, visible display slots, and thread-panel buttons.
- `internal/telegramcontrol/threads.go` owns the command-to-runtime bridge:
  create, target, reply lookup, summary queueing, ingress rebind, callback-message
  ledger writes, and absorb delegation.
- `internal/telegramruntime/session_scope.go` preserves thread scope when building
  runtime session targets; `internal/telegramruntime/ingress_replay.go` drops
  replayed work for closed or missing thread lanes.
- `runtime/telegram_threads.go` already implements absorb as a scoped summary
  and synthetic main-chat turn with provenance metadata. `runtime/doctor_threads.go`
  exposes thread count/status/session evidence to doctor output.
- Tests cover the important invariants: `session/store_telegram_threads_test.go`,
  `runtime/telegram_threads_test.go`,
  `runtime/continuation_scope_invariant_test.go`,
  `runtime/auto_approval_runtime_test.go`, and Telegram callback tests around
  continuation, memory, stream stop, approval windows, and pagination.

Current durable children already have most of the durable attachments:

- `core/durable_agents.go` stores canonical child identity and parent/review
  linkage; `session/store_durable_agents.go` persists it with policy hash/version,
  local roots, network policy, wake mode, secret scopes, and status.
- `core/durable_agent_policy.go` defines live policy, channel config, bootstrap
  ceiling, shared-context posture, tailnet posture, external-channel config, and
  ceiling validation.
- `core/durable_agent_continuity.go` holds recent interactions, pending
  questions, review refs, ratified outcomes, parent-child conversation, and
  setup wizard state; `session/store_durable_agent_state.go` persists runtime
  posture and continuity JSON.
- `core/durable_agent_wizard.go`, `tool/durable_agent_wizard.go`, and
  `internal/telegramcommands/commands_wizard.go` implement the current setup
  wizard and inline Telegram callbacks. The existing wizard is useful substrate,
  but it is external-channel oriented (`wizard_start` currently only supports
  `channel_kind=external_channel`) rather than a thread-promotion wizard.
- `internal/telegramcommands/commands_agents.go` already renders `/agents` cards
  with state-derived `Chat` and `Refresh` buttons, giving a projection surface
  for promoted durable threads.
- `tool/durable_agent_access_conversation.go`,
  `runtime/durable_wake_parent_conversation.go`, and
  `runtime/durable_group_context.go` provide parent-child conversation lanes and
  wake-time governor context.
- `runtime/durable_wake.go`, `runtime/durable_wake_scheduled_review.go`,
  `runtime/external_channel_wake.go`, `runtime/durable_child.go`, and
  `runtime/durable_group.go` synthesize scheduled, parent-conversation,
  external-channel, group, and child-executor turns.
- `durableagent/runtime.go`, `durableagent/remote_child.go`,
  `durableagent/remote_runtime.go`, and `durableagent/http.go` provide review
  artifact upload/queueing and remote-control plumbing.

The proposed direction is to stop treating those as separate operator worlds.
Instead, durable-agent records can become backing records for threads that need
features not available to a plain local side thread.

### Evidence-Backed Implementation Plan

The first implementation should be a doc-and-projection slice, not a broad
rewrite. The current code already supports the core lane and child substrates;
the missing object is a canonical promotion handoff that links them and makes
the operator choices inspectable.

1. **Add a promotion handoff record.** Introduce a small canonical schema for a
   thread promotion handoff rather than inferring promotion from chat text. It
   should include source `chat_id`, source `thread_id`, source thread session
   ID, created/approved actor, selected context summary, memory candidate refs,
   requested/approved/denied resource candidates, policy patch, optional backing
   durable-agent ID, first-task prompt, expected validation, status, and source
   refs. Existing patterns to mirror: `session.ReviewEvent`,
   `session.OperationState`, `session.OperationArtifact`, `session.RecordReference`,
   durable-agent review metadata, and `execution_events`.
2. **Project promotion state into `/threads`.** Extend the existing thread list
   projection rather than replacing `/threads`. A thread with no handoff remains
   cheap and local; a thread with a draft/approved/applied handoff gets compact
   `Promote`, `Continue`, or backlink affordances. Use `telegram_callback_messages`
   as the durability pattern for callbacks; do not trust textual `(thread N)`
   prefixes without the ledger.
3. **Build a thread-promotion wizard beside, not inside, the external-channel
   wizard.** Reuse wizard state/normalization/button patterns from
   `core.DurableAgentSetupWizardState`, `tool/durable_agent_wizard.go`, and
   `commands_wizard.go`, but define promotion-specific steps:
   context selection, memory candidates, resource candidates, policy defaults,
   backing-principal decision, first supervised task, and final handoff approval.
   The current durable-agent wizard can remain external-channel specific.
4. **Use explicit memory delegation.** Do not copy parent or thread memory into
   a child by default. Reuse the shape of `tool/durable_agent_memory.go`:
   candidate generation, operator-visible candidate IDs, explicit approval, and
   writes into the child memory root only after approval. Thread promotion can
   generate candidates from the source thread session and recent memory-review
   items, but the handoff should store selected refs and distilled content.
5. **Use capability requests/grants for resource transfer.** Reuse
   `session.CapabilityRequest`, `session.CapabilityGrant`,
   `session.DurableChildAgreement`, `tool/capability.go`, and durable-agent
   delegation request/report surfaces for filesystem, command, tool, network,
   credential, and external-account resources. Do not encode grants in prose.
6. **Keep child runtime materialization honest.** `core.ChildRuntimeContract` and
   `runtime/durable_child_sandbox.go` currently materialize executables,
   readonly paths/binds, secret binds, and parent environment variables from
   active grants. They do not model writable external worktree access. If the
   promoted child needs writable workspace resources, add that as an explicit
   new capability/materialization design rather than overloading readonly
   `child_runtime`.
7. **Queue a supervised first run only after handoff approval.** After the
   handoff is approved and a backing durable agent exists, use existing
   parent-conversation / wake machinery to pass the first task and validation
   expectations. Record the outcome in `execution_events` and review artifacts
   so `/status`, `/agents`, `/threads`, and `/doctor` can explain the result.
8. **Make doctor/status repair explicit.** Promotion should add enough typed
   links for `/doctor` to say: source thread exists/closed/open, handoff status,
   backing agent status, grants active/stale/failed, first-run status, and next
   repair action. This should reuse existing drift/blocked-reason patterns rather
   than adding opaque prose fields.

### Implementation Risks And Required Tests

- **Scope leakage:** thread-scoped approvals, memory focus, progress, and
  callbacks must not leak to the default chat or another thread. Existing tests
  around continuation scope, auto-approval scope, memory focus, and callback
  ledgers should be extended for promotion callbacks.
- **Authority leakage:** promotion must not turn text, thread membership, or a
  child-like name into capability. Tests should prove promotion without approved
  grants cannot access tools, credentials, network, writable paths, or external
  accounts.
- **Memory over-transfer:** generated memory candidates must remain candidates
  until approved. Tests should cover denied memory delegation and verify no child
  memory write occurs.
- **Resource over-transfer:** `child_runtime` should reject unsupported writable
  materialization until a new explicit contract exists. Tests should cover grant
  rendering, invalid contracts, stale/expired grants, and child-runtime block
  reporting.
- **Callback staleness:** wizard buttons must validate current step/status and
  target the durable callback message, matching the stale-step protections in the
  current durable wizard callbacks.
- **Recovery visibility:** interrupted promotion or failed first run should leave
  an inspectable handoff status and execution/review evidence, not just a chat
  transcript. Add tests for `/status`/doctor projection once the schema exists.

Suggested initial test set:

- `go test ./session -run 'Test.*TelegramThread|Test.*ReviewEvent|Test.*OperationState|Test.*Capability'`
- `go test ./runtime -run 'Test.*TelegramThread|Test.*Continuation.*Thread|Test.*AutoApprovalThread|Test.*CapabilityGrantWake|Test.*DurableWake|Test.*Doctor'`
- `go test ./internal/telegramcommands -run 'Test.*Thread|Test.*DurableWizard|Test.*Agents|Test.*Memory|Test.*Continuation.*Thread|Test.*ApprovalWindow'`
- `go test ./tool -run 'TestCapability|TestDurableAgentToolMemory|TestDurableAgentToolConversation|TestDurableWizard'`
- `go test ./durableagent -run 'Test.*ReviewArtifact|Test.*Remote.*Artifact'`

## Design Rules

- A plain thread must stay cheap to create and close.
- Durable features attach progressively; they must not be required for ordinary
  side-thread work.
- `/threads` should remain a lightweight work-lane board. Rich durable controls
  belong in `/agents` after promotion.
- Promotion must be one-tap for the default safe path and editable when policy
  details matter.
- Promotion must include reviewable context transfer. A child receives an
  approved memory digest and source refs, not raw parent/core memory by default.
- Promotion must include reviewable resource transfer. Filesystem, command,
  tool, network, credential, and external-contact boundaries should be visible
  before the promoted agent runs.
- Thread labels remain human-scale. Raw IDs stay in trace/detail surfaces.
- Every durable attachment has a typed record. Prose can propose; it cannot
  authorize.
- Capability expansion still goes through request, review, grant, expiry,
  revocation, and invocation evidence.
- Transport bindings are compiled, narrow adapters. This must not become an
  omnichannel plugin layer.
- Remote or isolated execution still needs a hard principal, storage boundary,
  bootstrap ceiling, and control-plane evidence. The operator can see it as a
  thread-backed runtime binding, not as a separate everyday object.
- Absorbing or closing a durable thread must have explicit semantics for wakes,
  bindings, pending approvals, capability grants, and remote enrollment.
- Absorbing a promoted agent should create an absorb review thread by default,
  using the next available lowest visible thread number.
- Parent-child reintegration should be conversational before it is executable:
  discuss first, compile a typed absorb plan, then apply or cancel.
- Absorb must not silently transfer child capabilities to the parent. Grants
  should default toward revoke, expire, or mark-stale unless the operator
  explicitly approves a new parent-scoped grant.

## Migration Shape

This should not be a flag-day deletion of durable agents. A safer sequence:

1. Document the thread-native model and review it against design principles.
2. Improve projections so `/threads`, `/status`, and `/health trace` can show
   durable attachments for a thread without changing authority semantics.
3. Add a `Promote` action to the thread projection that creates a default safe
   durable profile from the thread and links the two surfaces.
4. Add a promotion wizard for context/memory review, resource/command/network
   review, policy compilation, and first supervised handoff.
5. Store a canonical promotion handoff artifact so recovery and health surfaces
   can report actual selected context, grants, denied grants, and first task
   instead of inferring them from chat.
6. Make `/agents` render promoted durable work as cards with state-derived
   buttons for continue, wake, access, policy, details, and archive.
7. Add a canonical thread-profile/policy surface for local durable threads.
8. Let new local or scheduled durable work attach to a thread profile before it
   creates any backing durable-agent record.
9. Add an `Absorb` action to promoted agent cards that creates a fresh absorb
   review thread with bounded child context and roll-up material.
10. Add a typed absorb-plan surface for memory candidates, artifact indexes,
   grant disposition, wake teardown, binding disposition, and child archive
   state.
11. Introduce an internal backing-principal link for cases that need isolated
   sandbox identity, capability grants, or Tailnet enrollment.
12. Project legacy durable-agent records as backing principals attached to
   operator-visible threads where possible.
13. Keep durable-agent CLI commands for maintenance and migration until the new
   thread-native surfaces cover the operational need.

## Non-Goals

- Do not remove typed authority records.
- Do not weaken child-scoped credentials, storage roots, or sandboxing.
- Do not replace Tailnet node identity checks with thread names or chat text.
- Do not make `/thread` a generic plugin marketplace.
- Do not auto-write memory merely because a thread was absorbed or made durable.
- Do not hide forensic identifiers from maintenance and diagnosis surfaces.

## Open Questions

- Should the long-term canonical thread identity be Telegram-specific, or should
  Aphelion introduce a transport-neutral work-thread table with Telegram display
  slots as a projection?
- What is the exact principal string for grants to a durable thread:
  `thread:<id>`, `telegram_thread:<chat>:<id>`, or a backing durable principal?
- Should a thread profile be allowed without a backing principal, or should every
  durable thread immediately receive a backing principal record?
- What does `/absorb` mean for a scheduled thread: pause wakes, revoke wakes, or
  ask the operator?
- Should `Promote` close the source thread, leave it open as an alias, or ask
  after the promoted agent is created?
- What is the exact schema for a promotion handoff artifact, and which fields
  are required before a promoted agent may run?
- Which core-memory/context exposures should be summarized automatically during
  promotion, and which should require explicit operator selection?
- How should the wizard present resources the thread happened to use versus
  resources the child is actually allowed to keep?
- Should `/agents` replace `/threads` for promoted work entirely, or should
  `/threads` show promoted backlinks in a compact read-only form?
- Should absorb review threads always use the next available lowest visible
  thread number, or should there be a setting for main-chat review instead?
- What is the exact schema boundary between an absorb review conversation and
  the typed absorb plan it proposes?
- Which roll-up candidates should be generated automatically, and which should
  only appear after the parent asks for them?
- What child states block absorb entirely, such as active remote execution,
  unacknowledged control-plane enrollment, or destructive pending approvals?
- How should group-bound threads be represented when the Telegram group itself
  has a durable transcript and independent reply policy?
- Which legacy durable-agent fields belong directly on thread profile/policy,
  and which should remain only on backing runtime records?

## Review Criteria

The direction is working if:

- a new durable workflow starts as quickly as a side thread;
- adding wake, memory focus, or a capability feels like extending the current
  lane, not switching products;
- `/threads` stays compact, with promotion as the main bridge to durable
  controls;
- `/agents` becomes a state-driven durable work board rather than a setup-heavy
  registry;
- absorbing a promoted agent starts a focused parent review thread instead of
  silently merging child context;
- the absorb review conversation compiles to an explicit plan for memory,
  artifacts, grants, wakes, bindings, and archive state;
- `/status` and `/health trace` can point from visible thread to typed authority
  and execution evidence in one hop;
- ordinary threads do not inherit ambient capability;
- remote children remain strongly identified and bounded even when presented as
  thread-backed work.
