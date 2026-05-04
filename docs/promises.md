# Aphelion Promise Ledger

This ledger tracks public promises that can be mistaken for current behavior.
Status values:

- `implemented`: covered by live code and tests
- `partial`: useful behavior exists, but the public claim is broader than code
- `planned`: accepted target for the current implementation roadmap
- `future`: aspirational target, not current behavior

Every broad README or requirements claim should map here when the implementation
surface is not obvious from the code.

| Promise | Status | Current behavior | Next implementation target |
|---|---:|---|---|
| Single Linux daemon for Telegram, sessions, tools, and delivery | implemented | Telegram polling, SQLite sessions, progress, slash commands, maintenance loops, and delivery guards exist. | Keep covered by runtime and deployment tests. |
| Provider support for Anthropic, OpenAI, OpenRouter, Gemini, and Ollama | implemented | Native adapters, streaming, tool-call mapping, model-slot routing, and failover-chain config exist for these providers. | Keep provider request/stream/failover tests current; add live readiness probes if operator experience needs them. |
| Anthropic prompt caching with explicit cache breakpoints | partial | Structured `cache_control: ephemeral` is emitted for selected system/tool blocks, with config-backed strategy/off and TTL validation. | Add cache-aware pruning/lookback behavior if live cache churn requires it. |
| Single static binary | implemented | Normal build remains a fast developer binary; `make build-static` and release CI produce a statically linked Linux binary. | Keep static proof covered by release CI. |
| String anonymization / provider-visible identity control | partial | `identity.user_agent` is configurable and cache boundaries are structural, but some provider-visible names/tool descriptions are still hardcoded. | Add config-owned names and an opt-in anonymous profile with leak tests. |
| Config schema covers all documented knobs | partial | Runnable config is narrower than the broad requirements schema; unknown keys are accepted silently. | Warn on ignored/future keys in check-config, startup, and doctor. |
| Sandbox isolation for non-admin/durable execution | partial | Bubblewrap profiles isolate roots, hide secrets, clear env, drop caps, and can deny network. | Add config-backed profiles and honest warnings for deeper primitives not yet enforced. |
| Native constrained file tools and web fetch | planned | `exec`, memory/session tools, optional OpenAI storage, and governed external-tool lifecycle exist; native file/fetch tools do not. | Add scoped `read_file`, `write_file`, `list_dir`, `search`, then `fetch_url`. |
| Telegram media buttons for ambiguous voice/audio/video | planned | Automatic media handling exists for transcription, vision, PDFs/text, metadata, and outbound media. | Add config-enabled callback workflows with stale/timeout handling. |
| Release install/update path | implemented | Release workflow builds the static binary, install/update scripts run park-restart, config preflight, init, and verify-deploy. | Keep release scripts covered by deploy checks. |
