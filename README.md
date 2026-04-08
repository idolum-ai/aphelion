# Aphelion

A minimal, focused agent runtime for personal AI assistants.

## Philosophy

- **Only what we need.** No plugin marketplace, no multi-channel support, no enterprise features.
- **Linux only.** No macOS/Windows compat. Single target, no conditionals.
- **Undetectable by design.** Every identifiable string — project name, user-agent, system prompt markers — is configurable or absent.
- **Python.** Hackable, readable, forkable.
- **Anthropic-first caching.** Prompt caching done right: cache boundary splitting, 1h TTL, compaction that preserves prefixes.
- **File-based memory.** Workspace files are the source of truth. Vector search optional.

## Target Stack

- **Channels:** Telegram
- **Providers:** Anthropic (Claude), Google (Gemini), local models (Ollama), OpenAI (embeddings/fallback)
- **Tools:** exec, file ops, web fetch, memory search, sub-agent spawning
- **Voice:** ElevenLabs TTS
- **Automation:** heartbeat, cron

## Architecture

See `requirements/` for component specs. Each spec is designed, reviewed, then implemented via sub-agents.

## Status

Design phase. Specs in progress.
