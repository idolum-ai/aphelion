# Aphelion — Requirements Index

Status: ⬜ = not started, 🟡 = in progress, ✅ = done

## Foundation
1. ✅ [`core.md`](core.md) — Event loop, message lifecycle, error handling, shutdown
2. ✅ [`config.md`](config.md) — Config system, string anonymization, environment variables
3. ⬜ [`sessions.md`](sessions.md) — Conversation state, context window, compaction, cache-aware truncation

## LLM
4. ⬜ [`providers.md`](providers.md) — Anthropic, Gemini, OpenAI, Ollama adapters. Streaming. Prompt caching. Failover.
5. ⬜ [`thinking.md`](thinking.md) — Extended thinking / reasoning mode handling across providers

## Channels
6. ⬜ [`telegram.md`](telegram.md) — Bot API, polling/webhooks, formatting, media, reactions, groups, topics

## Agent
8. ⬜ [`tools.md`](tools.md) — Tool definition, dispatch, sandboxing, exec, file ops, web fetch
9. ⬜ [`memory.md`](memory.md) — Workspace files, bootstrap sequence, vector search, embeddings
10. ⬜ [`system-prompt.md`](system-prompt.md) — Prompt assembly, cache boundary, dynamic vs stable sections
11. ⬜ [`subagents.md`](subagents.md) — Spawning, communication, completion, isolation

## Automation
12. ⬜ [`heartbeat.md`](heartbeat.md) — Periodic agent turns, HEARTBEAT.md, task blocks, active hours
13. ⬜ [`cron.md`](cron.md) — Scheduled jobs, isolated sessions, delivery routing

## Media
14. ⬜ [`voice.md`](voice.md) — ElevenLabs TTS, when to speak vs text
15. ⬜ [`media.md`](media.md) — Image/audio/video handling, upload/download across channels

## Operations
16. ⬜ [`deployment.md`](deployment.md) — Systemd, process management, logging, restart behavior
17. ⬜ [`security.md`](security.md) — Credential storage, API key management, exec sandboxing, permissions
