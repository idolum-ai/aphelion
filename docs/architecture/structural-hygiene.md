# Structural Hygiene

Aphelion uses file size as a review signal, not as an automatic refactor order.
Large files are acceptable only when they have a clear durable responsibility and
an explicit split direction. New large files should be rare.

## Rules

- Files over 800 lines must appear in this ledger.
- A large file should have one owner concept, not a grab bag of unrelated flows.
- Split when a file mixes durable concepts, grows a second ownership boundary, or
  blocks local reasoning. Do not split only to satisfy a line counter.
- Delete completed plans and transient migration notes after their durable
  content is moved into current docs.

## Ledger

| File | Owner concept | Split direction |
|---|---|---|
| `tool/durable_agent.go` | Durable-agent tool surface and operator rendering | Split command handlers, renderers, and channel config editing. |
| `session/types.go` | Shared persisted record contracts | Move domain-specific record groups near their stores. |
| `config/config.go` | Runtime config schema, defaults, validation | Split provider, sandbox, autonomy, and path validation sections. |
| `tool/exec.go` | Built-in tool definitions and exec request handling | Move schema literals and durable-agent definition helpers out. |
| `runtime/doctor.go` | Operator doctor report assembly | Split source probes from report rendering. |
| `telegram_decisions.go` | Telegram decision and review callbacks | Split review events, artifacts, and generic decisions. |
| `memory/semantic.go` | Semantic memory indexing/search | Split ingestion, query, and prompt compaction. |
| `core/durable_agents.go` | Durable-agent core records and normalization | Split policy, channel config, continuity, and enrollment records. |
| `tool/tool_authority.go` | Tool lifecycle authority and drift | Split install/probe/audit authority paths. |
| `governorbackend/codex.go` | Codex provider adapter | Split request building, streaming, and tool-call conversion. |
| `runtime/runtime.go` | Runtime composition and high-level turn services | Continue moving turn/status/continuation helpers to narrower files. |
| `face/status_render.go` | Telegram status rendering | Split chat, system, durable, and authority renderers. |
| `session/store_sessions.go` | Session row persistence | Split message, sidecar, and search helpers. |
| `session/mission_ledger.go` | Mission ledger persistence | Split mission state, review, and status projection stores. |
| `session/store_durable_agents.go` | Durable-agent persistence | Split enrollment, policy updates, state, and config validation. |
| `prompt/builder.go` | Provider prompt assembly | Split section builders by contract type. |
| `main.go` | Binary startup and runtime assembly | Move provider/transport wiring behind small constructors. |
| `runtime/durable_group.go` | Durable Telegram group runtime | Split routing, sync, and delivery helpers. |
| `session/store_schema.go` | Current SQLite schema maintenance | Keep current schema version checks and current table/index creation. |
| `maintenance_repair.go` | Live-state repair commands | Split repair targets into separate command files. |
| `agency_eval.go` | Local agency evaluation harness | Split scenario definitions from scoring/rendering. |
| `runtime/codex_app_server_channel.go` | Codex app-server external-channel adapter | Split protocol IO, wake assembly, and status projection. |
| `commands.go` | Telegram command dispatch | Split callback handling and command groups. |
| `tool/capability.go` | Capability request/grant tool surface | Split request, review, grant, and render helpers. |
| `runtime/progress.go` | Telegram progress lifecycle | Split rendering from delivery state. |
| `telegram/client.go` | Telegram HTTP client | Split methods by Telegram API surface. |
| `runtime/continuation_operation_state.go` | Operation/continuation projection helpers | Split operation state from continuation repair helpers. |
| `maintenance_durable_agent.go` | Durable-agent CLI maintenance | Split subcommands. |
| `tool/update_operation.go` | Operation update tool | Split schema, validation, and state application. |
| `runtime/continuation_render.go` | Continuation prompt/rendering | Split button/control rendering from text summaries. |
| `provider/failover.go` | Provider failover orchestration | Split retry policy from stream fanout. |
| `face/operational.go` | Operational face rendering | Split stop/status/mission render paths. |
| `commands_status.go` | Telegram status command rendering | Split system/chat/durable views. |
| `maintenance_memory.go` | Memory maintenance CLI | Split import, GC, forget, and reset commands. |
| `provider/openai.go` | OpenAI provider adapter | Split request, media, stream, and tool conversion. |
| `session/store_execution.go` | Execution-event and turn-run persistence | Split TES from startup recovery run tracking. |
| `runtime/runtime_test_helpers.go` | Runtime test fixtures compiled into runtime package | Move helpers behind proper `_test.go` files. |
| `runtime/turn.go` | Runtime turn orchestration | Split command policy, delivery, and lifecycle glue. |
| `commands_model.go` | Model command UI | Split slot state, rendering, and mutation handlers. |
| `runtime/authority_projection.go` | Authority status projection | Split grants, leases, auto-approval, and tailnet findings. |
| `quickstart.go` | Quickstart/install orchestration | Split detection, config writing, and service install. |
| `decision/broker.go` | Decision broker lifecycle | Split storage reconciliation from timeout handling. |
| `session/store_tools.go` | Tool lifecycle persistence | Split install/probe/audit stores. |
| `provider/anthropic.go` | Anthropic provider adapter | Split prompt conversion, stream handling, and tool mapping. |
| `memory/instrumentation.go` | Memory instrumentation import/parsing | Split source adapters from entry normalization. |
| `maintenance_tailnet.go` | Tailnet CLI maintenance | Split subcommands and renderers. |
| `session/store.go` | SQLite store construction and base schema | Keep only schema creation and DB lifecycle. |
