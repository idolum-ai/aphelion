# Aphelion — Requirements Index

Status: ⬜ = not started, 🟡 = in progress, ✅ = done

## Foundation
1. ✅ [`core.md`](core.md) — Event loop, message lifecycle, error handling, shutdown
2. ✅ [`config.md`](config.md) — Config system, string anonymization, environment variables
3. ✅ [`principals.md`](principals.md) — Config-assigned Telegram DM principals, admission, authority
4. ✅ [`sessions.md`](sessions.md) — Conversation state, context window, compaction, cache-aware truncation
5. ✅ [`governor.md`](governor.md) — Governor backend, face pipeline, Codex-first core

## LLM
6. ✅ [`providers.md`](providers.md) — Inference adapters only. Streaming, retries, and failover staging.
7. ⬜ [`thinking.md`](thinking.md) — Extended thinking / reasoning mode handling across providers

## Channels
8. ✅ [`telegram.md`](telegram.md) — Bot API, polling/webhooks, formatting, media, reactions, groups, topics

## Agent
9. ⬜ [`tools.md`](tools.md) — Tool definition, dispatch, sandboxing, exec, file ops, web fetch
10. ✅ [`memory.md`](memory.md) — Workspace files, shared/per-user memory, OpenAI files, vector stores
11. ✅ [`system-prompt.md`](system-prompt.md) — Governor prompt, face prompt, cache boundary, dynamic vs stable sections
12. ✅ [`host.md`](host.md) — Face identity, anti-drift, Host-specific prompt files
13. ⬜ [`subagents.md`](subagents.md) — Spawning, communication, completion, isolation

## Automation
14. ⬜ [`heartbeat.md`](heartbeat.md) — Periodic agent turns, HEARTBEAT.md, task blocks, active hours
15. ⬜ [`cron.md`](cron.md) — Scheduled jobs, isolated sessions, delivery routing

## Media
16. ⬜ [`voice.md`](voice.md) — ElevenLabs TTS, when to speak vs text
17. ✅ [`media.md`](media.md) — Image/audio/video handling, OpenAI transcription/translation

## Operations
18. ⬜ [`deployment.md`](deployment.md) — Systemd, process management, logging, restart behavior
19. ✅ [`security.md`](security.md) — Trusted-admin floor, isolation boundaries, sandbox assembly, credential lifecycle, permission model
