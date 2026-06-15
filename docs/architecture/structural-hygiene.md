# Structural Hygiene

Aphelion uses file size as a review signal, not as an automatic refactor order.
Large files are acceptable only when they have a clear durable responsibility and
an explicit split direction. New large files should be rare.

## Rules

- Go files over 800 lines, including tests, must appear in this ledger.
- A large file should have one owner concept, not a grab bag of unrelated flows.
- Split when a file mixes durable concepts, grows a second ownership boundary, or
  blocks local reasoning. Do not split only to satisfy a line counter.
- Top-level packages and stable subpackages that own authority, credentials,
  durable state, transports, tools, or external effects must carry a `doc.go`
  ownership note that names what the package owns and what it must not import or
  decide. Tiny adapters, generated fixtures, and temporary internal leaves are
  exempt until they become a stable ownership boundary.
- Delete completed plans and transient migration notes after their durable
  content is moved into current docs.

## Ledger

| File | Owner concept | Split direction |
|---|---|---|
| `agent/turn.go` | Agent turn orchestration for provider interaction, tool calls, progress reporting, and final result assembly across one durable turn. | Split provider execution, tool-loop control, and finalization helpers when any of those subflows require independent lifecycle tests or authority boundaries. |
| `internal/telegramcommands/commands_continuation_callback_test.go` | Telegram continuation callback tests covering approval buttons, stale callbacks, retry prompts, and callback-render edge cases. | Split approval-window callbacks, retry callbacks, and stale/legacy callback scenarios when their fixtures no longer share the same Telegram callback harness. |
| `prompt/builder_test.go` | Prompt builder regression tests for runtime awareness, memory projections, tool manifests, and governor/face prompt surfaces. | Split stable prompt-file assembly, dynamic runtime awareness, and tool/capability manifest projection tests when one fixture family dominates the file. |
| `runtime/continuation_approval_trigger_test.go` | Continuation approval-trigger tests for follow-up phases, approval-window semantics, and guarded autoapproval boundaries. | Split approval-window trigger coverage from phase-plan continuation and follow-up proposal scenarios when their harnesses diverge. |
| `runtime/continuation_materialize_bundle_test.go` | Continuation materialization bundle tests for grouped phases, approval cards, and bundle validation behavior. | Split bundle construction tests from bundle validation/error cases when fixtures stop sharing the same materialization setup. |
| `runtime/continuation_materialize_phase_plan_test.go` | Phase-plan continuation materialization tests covering proposal cards, required capability grants, approval levels, and phase-plan state. | Split capability-grant materialization, approval-card rendering, and phase-plan state mutation tests when each grows a separate fixture boundary. |
| `runtime/continuation_required_capability_test.go` | Continuation tests for required capability grants and their interaction with approval phases and grant materialization. | Split grant-contract extraction from approval materialization scenarios when grant validation needs its own focused harness. |
| `runtime/eval.go` | Runtime eval definitions, fixtures, scenario loading, and evaluation execution for authority, continuation, and recovery behavior. | Split scenario registry/loading, runner execution, and result reporting when eval families require independent ownership or release cadence. |
| `runtime/eval_boundary_attack.go` | Transcript-driven boundary attack evals for the public bounty claims: attacker turns, approval-surface capture, and typed authority/evidence/capability oracles. | Split attacker replay, scenario definitions, and bounty oracles when one family starts changing independently or grows separate live-run/reporting needs. |
| `runtime/eval_test.go` | Runtime eval regression tests across authority, continuation, recovery, and goal-pursuit scenarios. | Split by eval family when fixture setup or assertion style stops sharing the same eval harness. |
| `runtime/eval_trajectory.go` | Trajectory eval construction and replay support for watched sessions and multi-step agency traces. | Split trace ingestion, trajectory normalization, and replay/evaluation logic when each becomes independently testable. |
| `runtime/goal_continuation_test.go` | Goal-continuation tests for typed interpretation claims, follow-up proposal inference, and fail-closed continuation behavior. | Split interpretation-claim parsing, candidate selection, and proposal materialization tests when their fixtures stop sharing one continuation harness. |
| `runtime/turn_finalize.go` | Turn finalization orchestration for completion, recovery, persistence, and outbound result handling. | Split persistence/finalizer state updates from outbound delivery and recovery classification when either side grows a separate authority boundary. |
| `runtime/work_executor_continuation_test.go` | Work-executor continuation tests covering recovery handoffs, work-result evidence, and continuation completion semantics. | Split recovery handoff coverage from evidence/result classification tests when their fixtures become independent. |
| `session/store_telegram_threads.go` | Telegram thread persistence, lookup, reply-chain tracking, and thread-selection state in the session store. | Split thread metadata storage from reply-chain traversal and selection-state helpers when migrations or queries diverge. |
| `session/store_turn_runs.go` | Durable turn-run persistence for provider execution, continuation state, progress events, and recovery metadata. | Split provider-run records, continuation snapshots, and progress-event storage when schema evolution separates their lifecycles. |
| `session/types_continuation.go` | Continuation state types, approval windows, leases, phase plans, and recovery metadata shared across runtime/session boundaries. | Split approval-window, lease, phase-plan, and recovery record types when one type family gains independent validation or storage rules. |
| `tool/update_operation_test.go` | update_operation tool tests covering operation state mutation, phase-plan transitions, evidence validation, and completion guards. | Split completion/evidence guards, phase-plan updates, and operation artifact handling when their fixtures stop sharing one tool invocation harness. |
| `tool/web_search.go` | Web search tool contracts, provider selection, request normalization, and result shaping behind authority-controlled invocation. | Split provider adapters, request/response shaping, and registration metadata when additional search providers or policy branches land. |
| `agent/turn_test.go` | Agent turn-loop tests covering provider replies, tool-call sequencing, parallel tool batches, observer events, retry behavior, and cancellation. | Split observer/parallelism scenarios from provider-error and planning-only retry scenarios when those fixture shapes stop sharing the same turn harness. |
| `config/config.go` | Config schema type declarations for the single-binary TOML contract, including identity, provider, transport, storage, integration, and runtime control records. | Split type groups by durable config domain only when edits require independent ownership; provider/integration families are the first candidates, while defaults and load normalization stay with the config package contract. |
| `config/load_defaults_test.go` | Config loading defaults, live example coverage, ignored-key behavior, and config parser compatibility tests. | Split broad default snapshots into domain-focused config test files when one config domain starts carrying most of the fixture surface. |
| `config/validate.go` | Config schema validation and operator-safe config error shaping. | Split durable sub-schema validators into focused files when validation logic starts crossing config-domain boundaries. |
| `internal/telegramcommands/commands_session_status_test.go` | Telegram command tests for operator session/status surfaces and adjacent command-card controls that share the same command router and sender fixtures. | Split lifecycle commands, status cards, and context/memory/agents callback scenarios when their fixture setup diverges or one surface grows beyond shared command-smoke coverage. |
| `runtime/continuation_operation_plan.go` | Runtime projection from operation phase plans to continuation approval boundaries: plan-lease construction, stale phase cleanup, phase bundle selection, and phase-to-continuation matching. | Split phase-plan lease construction from phase approval/budget classification when approval families, gate policy, or matching helpers grow independent tests or ownership. |
| `runtime/constitution_test.go` | Runtime delivery constitution, brokerage adaptation, media repair, and execution-evidence grounding tests. | Split brokerage convergence, media repair, and execution-grounding fixtures when one area needs independent setup or begins obscuring the delivery contract under test. |
| `runtime/auto_approval_window_test.go` | Runtime auto-approval window tests covering operator approval leases, approval-window offers, exact bindings, stale/replay repair, double/cancel controls, and thread-scope persistence. | Split legacy auto-approval lease behavior from approval-window offer lifecycle and exact-binding scenarios when their fixtures or failure modes stop sharing one approval-window harness. |
| `runtime/tool_progress_reporter.go` | Turn monitor and Telegram tool-progress rendering, delivery, controls, caching, and progress-event evidence. | Split event-monitor recording from Telegram progress rendering when either side grows a separate lifecycle or transport boundary. |
| `session/store_schema.go` | SQLite schema versioning, migrations, and idempotent table/index repair for durable session storage. | Split migration families by durable session concept when schema repair helpers start requiring different ownership boundaries. |
| `session/store_schema_migration_test.go` | SQLite schema migration compatibility tests for historical session database versions and backfills. | Split migration fixtures by version family or durable record family when compatibility setup stops fitting one chronological migration harness. |
| `tool/native_file_tools.go` | Native file, fetch, and extraction tool implementations under sandbox and authority ceilings. | Split fetch/network policy and document extraction into focused files while keeping native tool registration local. |
