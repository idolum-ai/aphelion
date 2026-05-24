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
| `runtime/constitution_test.go` | Runtime delivery constitution, brokerage adaptation, media repair, and execution-evidence grounding tests. | Split brokerage convergence, media repair, and execution-grounding fixtures when one area needs independent setup or begins obscuring the delivery contract under test. |
| `session/store_schema_migration_test.go` | SQLite schema migration compatibility tests for historical session database versions and backfills. | Split migration fixtures by version family or durable record family when compatibility setup stops fitting one chronological migration harness. |
