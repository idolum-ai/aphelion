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
| Anthropic prompt caching with explicit cache breakpoints | implemented | Structured `cache_control: ephemeral` is emitted for selected system/tool blocks, config-backed strategy/off and TTL validation are enforced, and `auto`/`hybrid` cache strategies shape prompt assembly with dynamic-file lookback while preserving authority/runtime blocks. | Keep cache-shaping tests current as prompt sections evolve. |
| Single static binary | implemented | Normal build remains a fast developer binary; `make build-static` and release CI produce a statically linked Linux binary. | Keep static proof covered by release CI. |
| String anonymization / provider-visible identity control | implemented | `identity.user_agent`, `identity.face_name`, and `identity.governor_name` are configurable; anonymous profile suppresses project-specific default user agents unless the operator explicitly sets one. Operational copy and provider-visible instructions avoid avoidable project/face labels outside intentional self-awareness and face-identity surfaces. | Keep leak tests current and separate local relationship names from outbound protocol identity. |
| Config schema distinguishes live keys from future knobs | implemented | `config.example.toml` is the live operator schema and is load-tested without ignored-key warnings. Broader requirements entries are explicitly future design notes; ignored keys remain valid but are surfaced as warnings in config load, startup/check-config logs, and `/doctor`. | Keep new live keys in `config.Config`, `config.example.toml`, config tests, and operator projections together. |
| Sandbox isolation for non-admin/durable execution | implemented | Config-backed bubblewrap profiles isolate roots, hide secrets, clear env, drop caps, and enforce network denial. Isolated `network=allowlist` is deliberately narrowed: startup, `--check-config`, `/status`, and `/doctor` report unavailable per-destination enforcement, while sandboxed process execution refuses to run rather than silently using host networking. | Add real per-destination allowlist enforcement only if a governed workflow needs networked non-admin execution. |
| Native constrained file tools and web fetch | implemented | Scoped `read_file`, `write_file`, `list_dir`, `search`, and `fetch_url` exist; file paths are checked against sandbox profile roots/hidden paths, fetch honors profile network denial, and isolated `allowlist` fetches are refused until per-destination enforcement exists. | Add destination-scoped network enforcement only if `allowlist` becomes an executable network policy again. |
| Telegram media handling without processing-choice buttons | implemented | Ordinary Telegram media routes immediately with session/reference retention by default. Voice/audio/video are normalized to agent-decide, ordinary photos/text/PDF-style documents avoid blocking retention selectors, and non-blocking keep buttons remain available for permanent/local retention. Exceptional files such as archives, executables, secrets, or oversized uploads still ask first. | Add provider-specific live probes for direct audio/video analysis quality if Gemini/native media use expands. |
| Release install/update path | implemented | Release workflow builds the static binary, install/update scripts run park-restart, config preflight, init, and verify-deploy. | Keep release scripts covered by deploy checks. |
| Done-done roadmap completion | planned | A normative convergence plan now fixes operator surfaces to Telegram/CLI, preserves internal RPC for child control only, requires self-validation gates, and requires one final release with one live migration/init call. | Promote each remaining roadmap target into implemented code and tests without adding dashboard/web operator surfaces or partial releases. |
