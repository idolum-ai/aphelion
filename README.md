# Aphelion

A minimal, focused agent runtime for personal AI assistants.

## Philosophy

- **Only what we need.** No plugin marketplace, no multi-channel support, no enterprise features.
- **Linux only.** No macOS/Windows compat. Single target, no conditionals.
- **Undetectable by design.** Every identifiable string — project name, user-agent, system prompt markers — is configurable or absent.
- **Go.** Single static binary. Linux-native syscalls. Goroutines for concurrency. Deploy = `scp`.
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

Runnable v0:

- Telegram DM-only polling
- Anthropic inference
- SQLite session persistence
- Shell execution tool (`exec`)
- Config-assigned principals
- Host-rendered replies
- Heartbeat and config-driven cron
- Telegram voice transcription + optional TTS replies

## Run

Config file default:

`~/.config/aphelion/config.toml`

Required principal bootstrap:

```toml
[principals.telegram]
admin_user_ids = [123456789]
approved_user_ids = []
```

Recommended workspace setting for this repo:

```toml
[agent]
workspace = "/home/sadasant_gmail_com/code/github.com/idolum-ai/aphelion"
```

If `agent.workspace` points somewhere broader such as your home directory, the `exec`
tool will use that broader directory as its default shell scope.

Workspace prompt files are read from `agent.workspace` on every turn. Current
supported files and defaults:

- Stable bootstrap files: `SOUL.md`, `IDENTITY.md`, `USER.md`, `AGENTS.md`, `TOOLS.md`, `BOOTSTRAP.md`
- Dynamic files: `MEMORY.md`, `HEARTBEAT.md`
- Daily notes: `memory/YYYY-MM-DD.md` for today and yesterday when `daily_notes = true`
- `MEMORY.md` falls back to `memory.md` if the uppercase file is missing

These are configured via:

```toml
[agent]
bootstrap_files = ["SOUL.md", "IDENTITY.md", "USER.md", "AGENTS.md", "TOOLS.md", "BOOTSTRAP.md"]
dynamic_files = ["MEMORY.md", "HEARTBEAT.md"]
bootstrap_max_chars = 20000
bootstrap_total_max_chars = 150000
daily_notes = true
daily_notes_dir = "memory"
```

Build and run:

```bash
make build
./bin/aphelion --config ~/.config/aphelion/config.toml
```

Or:

```bash
make run
```

Install the latest GitHub Release binary:

```bash
make install-release
```

Update a release-installed binary and restart the user service:

```bash
make update-release
```

## Background Service

Install as a user service:

```bash
make install-user-service
```

Useful commands:

```bash
systemctl --user status aphelion
systemctl --user restart aphelion
journalctl --user -u aphelion -f
```

Update after pulling new changes:

```bash
make update
```

If you install from GitHub Releases instead of source, point the service at the installed binary:

```bash
APHELION_EXEC=$HOME/.local/bin/aphelion APHELION_WORKDIR=$HOME ./scripts/install-user-service.sh
```
