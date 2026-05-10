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
| Config schema covers all documented knobs | partial | Runnable config is narrower than the broad requirements schema; ignored or future keys remain valid but are surfaced as warnings in config load, startup/check-config logs, and `/doctor`. | Continue wiring future schema knobs or narrow requirements where a knob is only aspirational. |
| Sandbox isolation for non-admin/durable execution | partial | Config-backed bubblewrap profiles isolate roots, hide secrets, clear env, drop caps, and can deny network. Startup, `--check-config`, `/status`, and `/doctor` warn when isolated execution is configured but unavailable or when a network allowlist is only policy intent. | Enforce per-destination network allowlists or narrow the schema to the network policies actually enforced. |
| Native constrained file tools and web fetch | implemented | Scoped `read_file`, `write_file`, `list_dir`, `search`, and `fetch_url` exist; file paths are checked against sandbox profile roots/hidden paths, fetch honors profile network denial, and readiness warnings flag configured allowlists that are not yet per-destination enforced. | Add destination-scoped network enforcement if `allowlist` remains in the schema. |
| Telegram media handling without processing-choice buttons | implemented | Ordinary Telegram media routes immediately with session/reference retention by default. Voice/audio/video are normalized to agent-decide, ordinary photos/text/PDF-style documents avoid blocking retention selectors, and non-blocking keep buttons remain available for permanent/local retention. Exceptional files such as archives, executables, secrets, or oversized uploads still ask first. | Add provider-specific live probes for direct audio/video analysis quality if Gemini/native media use expands. |
| Release install/update path | implemented | Release workflow builds the static binary, install/update scripts run park-restart, config preflight, init, and verify-deploy. | Keep release scripts covered by deploy checks. |
| Done-done roadmap completion | planned | A normative convergence plan now fixes operator surfaces to Telegram/CLI, preserves internal RPC for child control only, requires self-validation gates, and requires one final release with one live migration/init call. | Promote each remaining roadmap target into implemented code and tests without adding dashboard/web operator surfaces or partial releases. |
