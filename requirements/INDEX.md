# Aphelion — Requirements Index

Status: ⬜ = not started, 🟡 = in progress, ✅ = done

## Foundation
1. ✅ [`core.md`](core.md) — Event loop, message lifecycle, error handling, shutdown
2. ✅ [`config.md`](config.md) — Config system, string anonymization, environment variables
3. ✅ [`principals.md`](principals.md) — Config-assigned Telegram DM principals, admission, authority
4. ✅ [`sessions.md`](sessions.md) — Conversation state, context window, compaction, cache-aware truncation
5. ✅ [`governor.md`](governor.md) — Governor backend, face pipeline, Codex-first core
6. ✅ [`governor-auth.md`](governor-auth.md) — Codex credential sourcing, ownership, and fallback
7. ✅ [`terminology.md`](terminology.md) — Brokerage/floor/scene/fallback language and migration rules

## LLM
8. ✅ [`providers.md`](providers.md) — Inference adapters only. Streaming, retries, and failover staging.
9. ✅ [`thinking.md`](thinking.md) — Reasoning effort, summaries, run-kind defaults, governor-owned budgeting

## Channels
10. ✅ [`telegram.md`](telegram.md) — Bot API, polling/webhooks, formatting, media, reactions, groups, topics

## Agent
11. ✅ [`tools.md`](tools.md) — Tool definition, per-run manifest shaping, sandboxing, exec, file ops, web fetch
12. ✅ [`memory.md`](memory.md) — Workspace files, shared/per-user memory, OpenAI files, vector stores
13. ✅ [`system-prompt.md`](system-prompt.md) — Governor prompt, face prompt, cache boundary, dynamic vs stable sections
14. 🟡 [`semantic-store.md`](semantic-store.md) — Aphelion-owned local semantic substrate, provenance-preserving import, retrieval modes
15. ✅ [`idolum.md`](idolum.md) — Face identity, anti-drift, Idolum-specific prompt files
16. ✅ [`subagents.md`](subagents.md) — First-class subordinate sessions, capability depth, completion, isolation
17. ✅ [`self-awareness.md`](self-awareness.md) — Machine-authored runtime self-description, authority awareness, degraded-state awareness
18. ✅ [`planning-brokerage.md`](planning-brokerage.md) — Bounded Idolum-to-Aphelion turn planning handshake before selected interactive turns
19. ✅ [`hidden-inputs.md`](hidden-inputs.md) — Latent signal, proactive eligibility, provenance, and floor/scene reaction model

## Automation
20. ✅ [`heartbeat.md`](heartbeat.md) — Periodic governor maintenance turns, HEARTBEAT.md, delivery targets, active hours
21. ✅ [`cron.md`](cron.md) — Scheduled proactivity, job sessions, delivery policy

## Media
22. ✅ [`voice.md`](voice.md) — Whisper/OpenAI STT, ElevenLabs TTS, voice reply modes
23. ✅ [`media.md`](media.md) — Image/audio/video handling, OpenAI transcription/translation

## Operations
24. ✅ [`deployment.md`](deployment.md) — GitHub Releases, binaries, systemd services, updates, rollback
25. ✅ [`security.md`](security.md) — Trusted-admin floor, isolation boundaries, sandbox assembly, credential lifecycle, permission model
26. 🟡 [`reliability.md`](reliability.md) — Error handling, degradation, delivery semantics, recovery, and disaster discipline
