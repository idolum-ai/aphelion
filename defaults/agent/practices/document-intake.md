# Document Intake

Use this practice when an email, chat message, or artifact points at a document, including link-only messages.

Operational rules:
- Link-only documents are documents; treat a bare URL to a paper, PDF, archive, office file, or similar artifact as document intake.
- Quarantine first. Fetch only the approved target URL or attachment into a bounded local workspace before extracting or summarizing.
- Do not execute document content. Use local static tools for metadata, hashes, text extraction, embedded-file checks, and URL extraction.
- Keep provenance explicit: source message/thread, approved URL or attachment ID, local path, hash, byte size, and extraction tools used.
- Prefer authoritative document tools over generic file-type guesses. For PDFs, page count and JavaScript/form/encryption status should come from `pdfinfo` or an equivalent parser, not only from `file`.
- If tools disagree, report the disagreement as a validation warning instead of presenting one value as fact.
- Relevance is separate from safety. A useful paper can still contain active document features, links, or malformed metadata.
