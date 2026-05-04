# Aphelion — Requirements Index

Status: ⬜ = not started, 🟡 = in progress, ✅ = done

As-built architecture docs: [`../docs/architecture/`](../docs/architecture/README.md)

## Foundation
1. ✅ [`core.md`](core.md) — Event loop, message lifecycle, error handling, shutdown
2. 🟡 [`config.md`](config.md) — Config system, string anonymization, environment variables; broad schema gaps tracked in [`../docs/promises.md`](../docs/promises.md)
3. ✅ [`principals.md`](principals.md) — Config-assigned Telegram DM principals, admission, authority
4. ✅ [`sessions.md`](sessions.md) — Conversation state and truth-class contract (canonical/projection/operational current-state/compatibility fallback)
5. ✅ [`governor.md`](governor.md) — Governor backend, face pipeline, Codex-first core
6. ✅ [`governor-auth.md`](governor-auth.md) — Codex credential sourcing, ownership, and fallback
7. ✅ [`terminology.md`](terminology.md) — Brokerage/floor/scene/fallback language plus normative truth-class taxonomy

## LLM
8. 🟡 [`providers.md`](providers.md) — Inference adapters only. Streaming, retries, failover staging, and planned Gemini/Ollama parity.
9. ✅ [`thinking.md`](thinking.md) — Reasoning effort, summaries, run-kind defaults, governor-owned budgeting

## Channels
10. 🟡 [`telegram.md`](telegram.md) — Bot API, polling/webhooks, formatting, media, reactions, groups, topics; media-button workflow remains planned.

## Agent
11. 🟡 [`tools.md`](tools.md) — Tool definition, per-run manifest shaping, sandboxing, exec, planned native file ops and web fetch
12. ✅ [`memory.md`](memory.md) — Workspace files, shared/per-user memory, OpenAI files, vector stores
13. ✅ [`system-prompt.md`](system-prompt.md) — Governor prompt, face prompt, cache boundary, dynamic vs stable sections
14. 🟡 [`semantic-store.md`](semantic-store.md) — Aphelion-owned local semantic substrate, provenance-preserving import, retrieval modes
15. ✅ [`language.md`](language.md) — Shared house writing substrate across governor and face
16. 🟡 [`durable-agents.md`](durable-agents.md) — External sensory organs, quarantine, admin-ratified drift, and parent governance
17. ✅ [`idolum.md`](idolum.md) — Face identity, anti-drift, Idolum-specific prompt files
18. ✅ [`subagents.md`](subagents.md) — First-class subordinate sessions, capability depth, completion, isolation
19. ✅ [`self-awareness.md`](self-awareness.md) — Machine-authored runtime self-description, authority awareness, degraded-state awareness
20. ✅ [`planning-brokerage.md`](planning-brokerage.md) — Bounded Idolum-to-Aphelion turn planning handshake before selected interactive turns
21. ✅ [`hidden-inputs.md`](hidden-inputs.md) — Latent signal, proactive eligibility, provenance, and floor/scene reaction model
22. ✅ [`artifacts.md`](artifacts.md) — Channel-neutral file/media artifact model, capability envelope, retention classes
23. ✅ [`artifact-brokerage.md`](artifact-brokerage.md) — Bounded Idolum/Aphelion deliberation over artifact meaning, handling, and retention
24. ✅ [`operations.md`](operations.md) — Session-native operational work, proposals, material gates, and operational current-state boundaries

## Automation
25. ✅ [`heartbeat.md`](heartbeat.md) — Periodic governor maintenance turns, HEARTBEAT.md, delivery targets, active hours
26. ✅ [`cron.md`](cron.md) — Scheduled proactivity, job sessions, delivery policy

## Media
27. ✅ [`voice.md`](voice.md) — Whisper/OpenAI STT, ElevenLabs TTS, voice reply modes
28. 🟡 [`media.md`](media.md) — Image/audio/video handling, OpenAI transcription/translation; ambiguous-media buttons remain planned.

## Operations
29. ✅ [`deployment.md`](deployment.md) — GitHub Releases, static binary target, systemd services, updates, rollback
30. 🟡 [`security.md`](security.md) — Trusted-admin floor, isolation boundaries, sandbox assembly, credential lifecycle, permission model; config-backed sandbox profiles remain planned.
31. 🟡 [`reliability.md`](reliability.md) — Error handling, degradation, delivery semantics, recovery, and disaster discipline
32. ✅ [`turn-pipeline-refactor.md`](turn-pipeline-refactor.md) — Plan for aligning live package ownership with the runtime/turn/pipeline architecture
