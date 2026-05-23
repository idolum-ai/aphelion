# Structural Hygiene

Aphelion uses file size as a review signal, not as an automatic refactor order.
Large files are acceptable only when they have a clear durable responsibility and
an explicit split direction. New large files should be rare.

## Rules

- Go files over 800 lines, including tests, must appear in this ledger.
- A large file should have one owner concept, not a grab bag of unrelated flows.
- Split when a file mixes durable concepts, grows a second ownership boundary, or
  blocks local reasoning. Do not split only to satisfy a line counter.
- Broad packages with stable boundaries must carry a `doc.go` ownership note
  that names what the package owns and what it must not import or decide.
- Delete completed plans and transient migration notes after their durable
  content is moved into current docs.

## Ledger

| File | Owner concept | Split direction |
|---|---|---|
| `agent/turn_test.go` | Agent turn-loop tests covering provider replies, tool-call sequencing, parallel tool batches, observer events, retry behavior, and cancellation. | Split observer/parallelism scenarios from provider-error and planning-only retry scenarios when those fixture shapes stop sharing the same turn harness. |
| `config/load_defaults_test.go` | Config loading defaults, live example coverage, ignored-key behavior, and config parser compatibility tests. | Split broad default snapshots into domain-focused config test files when one config domain starts carrying most of the fixture surface. |
| `config/validate.go` | Config schema validation and operator-safe config error shaping. | Split durable sub-schema validators into focused files when validation logic starts crossing config-domain boundaries. |
| `runtime/constitution_test.go` | Runtime delivery constitution, brokerage adaptation, media repair, and execution-evidence grounding tests. | Split brokerage convergence, media repair, and execution-grounding fixtures when one area needs independent setup or begins obscuring the delivery contract under test. |
| `runtime/tool_progress_reporter.go` | Turn monitor and Telegram tool-progress rendering, delivery, controls, caching, and progress-event evidence. | Split event-monitor recording from Telegram progress rendering when either side grows a separate lifecycle or transport boundary. |
| `session/store_schema.go` | SQLite schema versioning, migrations, and idempotent table/index repair for durable session storage. | Split migration families by durable session concept when schema repair helpers start requiring different ownership boundaries. |
| `session/store_schema_migration_test.go` | SQLite schema migration compatibility tests for historical session database versions and backfills. | Split migration fixtures by version family or durable record family when compatibility setup stops fitting one chronological migration harness. |
| `telegram_decisions_busy_test.go` | Telegram busy-decision queueing, restart reconciliation, scoping, and callback behavior tests. | Split restart-reconciliation or polling-starvation scenarios into focused test files when busy-decision fixtures stop sharing setup shape. |
| `telegram_decisions_exec_approval_test.go` | Telegram exec-approval prompt, approval confirmation, expansion, timeout, actor, and approval-window offer tests. | Split approval-window offer or stale/restart scenarios into focused test files when exec-approval behavior gains an additional durable surface. |
| `tool/native_file_tools.go` | Native file, fetch, and extraction tool implementations under sandbox and authority ceilings. | Split fetch/network policy and document extraction into focused files while keeping native tool registration local. |
