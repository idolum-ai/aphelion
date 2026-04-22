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

Execution transparency:

- Runtime execution truth is recorded in TES (`execution_events`) with ordered, per-session event sequencing.
- `/status` and `/debug` are TES-first projections with compatibility fallbacks where legacy rows still exist.
- Final reply and continuation/debug summaries use grounding guards to avoid surfacing claims that are not evidenced by TES.

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
- Default daily-review durable child (`idolum-daily-review`) that wakes daily, stages yesterday's transcript into child-local files, and opens a scheduled child-to-parent check-in
- Telegram voice transcription + optional TTS replies
- Telegram slash commands: `/start`, `/help`, `/status`, `/debug`, `/agents`, `/memory`, `/stop`, `/new`, `/detach`, `/restart`, `/reinstall`, `/set_persona_model`, `/set_governor_effort`

## Run

Config file default:

`~/.aphelion/aphelion.toml`

Config and service boundary:

- `~/.aphelion/aphelion.toml` is Aphelion runtime config (providers, Telegram behavior, agent paths, session DB, memory/workspace roots).
- `~/.config/systemd/user/aphelion.service` is OS service config (how Linux starts/restarts Aphelion, working directory, and `ExecStart` command).
- The service points to the config file via `ExecStart=... --config ...`; changing one does not replace the other.

Service install overrides:

- `APHELION_CONFIG`: config file path passed to `--config` during service install/update.
- `APHELION_EXEC`: binary path to run in the unit (`ExecStart`).
- `APHELION_WORKDIR`: working directory written to the unit.

Required principal bootstrap:

```toml
[principals.telegram]
admin_user_ids = [123456789]
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
are read from `agent.shared_memory_root` for admin/global turns and from principal-local
memory roots for isolated child principals. Current supported files and defaults:

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

Inspect durable-agent inventory and health:

```bash
./bin/aphelion durable-agent list --config ~/.aphelion/aphelion.toml
./bin/aphelion durable-agent health --config ~/.aphelion/aphelion.toml --agent family-group
```

Configure a durable Telegram group agent:

1. Ask the admin agent to gather the target Telegram `chat_id` and participant `user_id` values.
2. Add an entry under `[[telegram.durable_groups]]` in `~/.aphelion/aphelion.toml`.
3. Rebuild/reinstall/restart so the poller loads the new group admission route.
4. Grant non-admin participant access on the durable agent (`durable_agent access_grant`).
5. Validate behavior in-group by mention/reply (or set `respond_on = "all"`).

Example config shape:

```toml
[[telegram.durable_groups]]
chat_id = -1001234567890
agent_id = "research-group"
charter = "Help the group investigate topics and summarize findings without widening standing authority."
respond_on = "mentions"         # "mentions" | "all"
review_target_chat_id = 123456789
llm_backend = "native"          # "native" | "codex"
llm_provider = "openrouter"     # required for native
llm_api_key = "..."
llm_model = "anthropic/claude-sonnet-4-6"
```

Current behavior note:

- The durable setup wizard surfaces bootstrap source plus effective backend/provider/model for clarity; model pinning is only offered when the effective bootstrap backend is native.
- Telegram group admission is still driven by `telegram.durable_groups` config and restart.

Daily-review durable child staging path (per child workspace):

`<durable-workspace>/.aphelion/daily-review/YYYY-MM-DD/transcript.md`

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
- `/status` prepends a concise `Quick Read:` summary when a native provider key is available, then shows humanized status telemetry labels
- admins also get `System Overview`, `Hot Chats`, `Find Chat`, and `Durables` buttons from `/status`
- `/stop` cancels the current turn and clears any queued follow-up messages for that chat
- `/new` starts a fresh chat session context for this chat without clearing memories
- `/detach` clears active/queued/continuation state and detaches pending approvals for you in this chat
- `/restart` (admin only) forces gateway restart and detaches pending approvals before exit by default
- when `memory.aggressive.flush_on_session_boundary = true`, `/stop`, `/new`, and `/restart` trigger a bounded session-memory flush before boundary actions complete
- background runtime loop failures (durable wake, heartbeat, cron, stale watchdog, idle expiry) are surfaced to admin DMs as `System warning` messages with repeat-throttling

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
