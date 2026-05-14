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
| `config/validate.go` | Config schema validation and operator-safe config error shaping. | Split durable sub-schema validators into focused files when validation logic starts crossing config-domain boundaries. |
| `tool/native_file_tools.go` | Native file, fetch, and extraction tool implementations under sandbox and authority ceilings. | Split fetch/network policy and document extraction into focused files while keeping native tool registration local. |
