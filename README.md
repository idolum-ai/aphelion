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
- **Providers:** Anthropic (Claude), Google (Gemini), local models (Ollama), OpenAI (platform storage/embeddings/fallback)
- **Tools:** exec, curated memory, session recall, optional OpenAI storage tools
- **Voice:** ElevenLabs TTS
- **Automation:** heartbeat, cron

## Architecture

Live package ownership:

- `runtime`: house shell (transport wiring, scopes/locks, background loops)
- `turn`: one-turn state machine (policy + stage order + commit contracts)
- `pipeline`: governor/face conversational transforms (brokerage/floor/render contracts)

```text
Telegram transport
   -> runtime (shell + adapters)
      -> turn.Machine (stage ordering)
         -> pipeline helpers/contracts (brokerage/floor/render)
      -> session persistence + outbound delivery ports
```

Details:

- [docs/architecture/README.md](docs/architecture/README.md)
- [runtime/README.md](runtime/README.md)
- [turn/README.md](turn/README.md)
- [pipeline/README.md](pipeline/README.md)
- [docs/telegram-ui-features.md](docs/telegram-ui-features.md)
- `requirements/` specs for component-level behavior

## Status

Runnable v0:

- Telegram DM-only polling
- Anthropic inference
- SQLite session persistence
- Shell execution tool (`exec`)
- Curated memory tool (`memory`) and transcript recall tool (`session_search`)
- Optional OpenAI file storage and vector-store tools
- Config-assigned principals
- Idolum-rendered replies
- Telegram typing + real tool-backed progress feedback
- Heartbeat and config-driven cron
- Telegram voice transcription + optional TTS replies
- Telegram slash commands: `/start`, `/help`, `/status`, `/stop`, `/detach`, `/restart`, `/reinstall`, `/set_persona_model`, `/set_governor_effort`

## Run

Config file default:

`~/.aphelion/aphelion.toml`

Legacy fallback:

`~/.config/aphelion/config.toml`

Required principal bootstrap:

```toml
[principals.telegram]
admin_user_ids = [123456789]
approved_user_ids = []
```

Recommended root layout for this repo:

```toml
[agent]
prompt_root = "~/.aphelion/agent"
exec_root = "/home/sadasant_gmail_com/code/github.com/idolum-ai/aphelion"
shared_memory_root = "~/.aphelion/agent"
user_workspace_root = "~/.aphelion/state/isolated/workspaces"
user_memory_root = "~/.aphelion/state/isolated/memory"
```

If `agent.exec_root` points somewhere broader such as your home directory, the `exec`
tool will use that broader directory as its default shell scope. Keep `prompt_root`
and `exec_root` separate.

Prompt files are read from `agent.prompt_root` on every turn. Dynamic memory files
are read from `agent.shared_memory_root` for admin turns and from the isolated
per-user memory root for approved-user turns. Current supported files and defaults:

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
daily_notes_dir = "memory/daily"
```

Build and run:

```bash
make build
./bin/aphelion --config ~/.aphelion/aphelion.toml
```

Optional OpenAI platform storage:

```toml
[providers.openai]
api_key = "sk-..."
base_url = "https://api.openai.com/v1"
model = "gpt-5.4"
max_tokens = 16384
context_window = 128000

[openai.files]
enabled = true
purpose = "assistants"

[openai.vector_stores]
enabled = true
default_store = ""
```

When enabled, Aphelion exposes two admin-facing auxiliary tools:

- `openai_file`
- `openai_vector_store`

These do not replace local files, curated memory, or SQLite session history. They extend the system with explicit external storage and retrieval helpers.

Or:

```bash
make run
```

Validate config only:

```bash
make check-config
```

Seed any missing prompt files under `agent.prompt_root`:

```bash
make init
```

Inspect effective paths and loaded prompt files:

```bash
make paths
```

Safe maintenance cleanup:

```bash
make gc
```

`make install-user-service`, `make update`, and `make update-release` also run
`aphelion init` automatically. Missing starter files are created under
`agent.prompt_root`, but existing files are never overwritten.

Targeted cleanup:

```bash
./bin/aphelion forget --config ~/.aphelion/aphelion.toml --chat 123456789
./bin/aphelion forget --config ~/.aphelion/aphelion.toml --principal 123456789
./bin/aphelion forget --config ~/.aphelion/aphelion.toml --shared-memory
```

Reset runtime or memory state without touching constitution files or config:

```bash
./bin/aphelion reset --config ~/.aphelion/aphelion.toml --scope runtime
./bin/aphelion reset --config ~/.aphelion/aphelion.toml --scope memory
./bin/aphelion reset --config ~/.aphelion/aphelion.toml --scope all
```

Install the latest GitHub Release binary:

```bash
make install-release
```

Update a release-installed binary and restart the user service:

```bash
make update-release
```

## Local Deploy

The local deploy path is the same one the service uses in production on this
machine:

1. build or replace the binary
2. run `--check-config`
3. run `init`
4. restart the user service
5. run `verify-deploy`

Source install / local redeploy:

```bash
make update
```

Release install / local redeploy:

```bash
make update-release
```

Manual equivalent for a source build:

```bash
go build -o bin/aphelion .
./bin/aphelion --config ~/.aphelion/aphelion.toml --check-config
./bin/aphelion init --config ~/.aphelion/aphelion.toml
systemctl --user restart aphelion
./bin/aphelion verify-deploy --config ~/.aphelion/aphelion.toml
```

`verify-deploy` is the post-restart gate. It runs a small synthetic turn through
the live governor/Idolum path, checks one tool path, and confirms persistence.
If it fails, the deploy should be treated as failed.

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

Inside Telegram:

- `/start` shows the intro and command help
- `/help` shows the command list
- `/status` opens a button-driven status view (`This Chat`, `Pending Only`, `Refresh`)
- admins also get `System Overview`, `Hot Chats`, and `Find Chat` buttons from `/status`
- `/stop` cancels the current turn and clears any queued follow-up messages for that chat
- `/detach` clears active/queued/continuation state and detaches pending approvals for you in this chat
- `/restart` (admin only) forces gateway restart and detaches pending approvals before exit by default

Config failures now exit once with a dedicated config error and the service will not
crash-loop. `make install-user-service`, `make update`, and `make update-release`
also run a config preflight, `init`, and `verify-deploy` around the service start.

Update after pulling new changes:

```bash
make update
```

If you install from GitHub Releases instead of source, point the service at the installed binary:

```bash
APHELION_EXEC=$HOME/.local/bin/aphelion APHELION_WORKDIR=$HOME ./scripts/install-user-service.sh
```
