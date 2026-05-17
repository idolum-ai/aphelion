# Config — Configuration & String Anonymization

## Overview

Aphelion's config is a single TOML file that controls the entire runtime. Every identifiable string — project name, user-agent headers, system prompt markers — is either configurable or absent. The goal: no provider can fingerprint the harness from the traffic it sees.

The live schema is the TOML shape accepted by `config.Config` and represented
by `config.example.toml`; that example is load-tested with no ignored-key
warnings. This requirements file also preserves future configuration homes so
the design does not collapse around the initial working path. Keys shown outside
the live schema are future design notes: if an operator puts them in today's
config, startup/check-config, `/status`, and `/health diagnose` must surface ignored-key
warnings rather than imply active behavior.

This spec therefore distinguishes between:

- **Live schema**: keys accepted by `config.Config` and covered by `config.example.toml`.
- **Implemented behavior**: the subset currently honored by the runtime.
- **Future knobs**: design-reserved keys that may appear here before they are wired, but must never silently misrepresent active behavior.

## Config File

Default location: `~/.aphelion/aphelion.toml`

Override via `APHELION_CONFIG` env var or `--config` flag.

Operators should also have an explicit initialization path, e.g. `aphelion init`,
that seeds missing prompt files under `agent.prompt_root` without overwriting
existing files.

Telegram progress configuration is intentionally split into:

- **whether** progress appears at all (`tool_progress`)
- **how** it is rendered (`tool_progress_style`)
- **how much** of it remains visible (`tool_progress_window`)

The default operator experience should favor semantic, readable progress over raw command dumps.

Implementation is no longer documented as a broad hidden schema contract. Live
operator keys belong in `config.Config` and `config.example.toml`; future homes
may be described here only as design notes and must produce ignored-key
warnings if used in today's config.

Autonomy is a config-owned policy, not a prompt convention. `default_mode` and
`ceiling` use `off`, `review_only`, `ask_first`, `leased`, or `mission`; the
configured default must not exceed the ceiling. In the current runtime this
policy is validated and projected through CLI, `/auto limits`, `/status`, and
`/health diagnose`. `leased` live overrides are implemented as bounded auto mode
records and cannot exceed the configured ceiling or maximum duration. Approval
grants remain separate spendable prompt budget. If config later tightens below
`leased` or disables live overrides, existing mode records become inert and
doctor reports the blocked precedence.

```toml
# ─── Identity ───
# These strings appear in HTTP headers.
# Set them to whatever you want, or leave blank.
[identity]
user_agent = ""          # HTTP User-Agent. Empty = generic Go default; anonymous_profile suppresses Aphelion-specific defaults.
project_name = ""        # Never sent to providers. Used only in local logs.
anonymous_profile = false # Use generic local names and outbound defaults unless explicit names/user_agent are set.

# ─── Telegram ───
[telegram]
bot_token = ""
detach_pending_on_restart = true # Clear durable pending decisions before restart exits
allowed_chats = []       # Chat IDs allowed to interact. Empty = allow all.
poll_timeout = 30        # Long-poll timeout in seconds
# Formatting
max_message_length = 4096  # Telegram's limit; auto-split longer messages
parse_mode = "MarkdownV2" # Default parse mode
tool_progress = "all"      # "all" | "new" | "off"
tool_progress_style = "semantic"  # "semantic" | "raw"
tool_progress_window = 4   # Rolling visible steps in the live progress message

[telegram.media]
download_max_size = "20MB"
auto_vision_photos = true
auto_vision_documents = true
extract_pdf_text = true
max_pdf_bytes = "8MB"

# Admitted durable Telegram groups are child-local durable agents, not house-principal chats.
# They require an explicit child-local LLM bootstrap and do not inherit the parent's governor credentials.
[[telegram.durable_groups]]
chat_id = -1001234567890
agent_id = "research-group"
charter = "Help locally in the group without widening standing role or authority."
respond_on = "mentions"         # "mentions" | "all"
review_target_chat_id = 123456789

# Child-local node LLM bootstrap.
# Choose either a native provider bootstrap or a Codex bootstrap.
llm_backend = "native"          # "native" | "codex"

# Native child bootstrap
llm_provider = "openrouter"     # "anthropic" | "openai" | "openrouter" | "gemini" | "ollama"
llm_api_key = ""
llm_base_url = "https://openrouter.ai/api/v1"
llm_model = "anthropic/claude-sonnet-4-6"
llm_max_tokens = 64000

# Codex child bootstrap
# llm_backend = "codex"
# llm_codex_auth_source = "codex_cli"  # "auto" | "codex_cli"
# llm_codex_home = "/srv/research-group/.codex"
# llm_codex_base_url = "https://chatgpt.com/backend-api"

# ─── Providers ───
[providers]
selection = "auto"                         # "auto" | "manual"
auto_order = ["openai", "anthropic", "openrouter"]  # gemini/ollama can be added explicitly.
default = ""                               # Optional manual primary.
# fallback_chain is omitted in auto mode so configured providers after the primary become fallbacks.
# For a manual chain, set selection = "manual", default, and fallback_chain explicitly.

# Failover behavior
[providers.failover]
max_retries = 3                  # Per-provider retries before failover
retry_backoff_base = 1.0         # Seconds, doubled each retry
retry_backoff_max = 30.0         # Cap on backoff delay
restore_primary = true           # Try primary again on next turn after failover

# Token estimation (for pre-flight context checks, not billing)
chars_per_token = 4              # Rough estimate; actual usage from provider response

[providers.anthropic]
api_key = ""
model = "claude-sonnet-4-6"     # Current flagship: claude-opus-4-6 (1M ctx), claude-sonnet-4-6 (1M ctx), claude-haiku-4-5 (200K ctx)
max_tokens = 64000               # Sonnet 4.6 max output: 64K. Opus 4.6: 128K.
context_window = 1000000         # Opus 4.6 and Sonnet 4.6: 1M tokens. Haiku 4.5: 200K.
anthropic_version = "2023-06-01"

# Cache strategy (see providers.md for full architecture)
cache_strategy = "hybrid"        # "auto" | "explicit" | "hybrid" | "off"
cache_ttl = "1h"                 # "5m" (1.25x write cost) or "1h" (2x write cost, 10x cheaper reads)
min_cache_tokens = 2048          # Minimum cacheable prefix. Opus 4.6/4.5: 4096, Sonnet 4.6: 2048, Haiku 4.5: 4096

# Cache-TTL pruning: trim old tool results after TTL to shrink re-cache writes
cache_ttl_pruning = true
pruning_soft_age = 10            # Soft-trim tool results older than N turns (head+tail with ...)
pruning_hard_age = 20            # Hard-clear tool results older than N turns ([trimmed])

# Block lookback safety: auto-inject breakpoint before hitting Anthropic's 20-block window
lookback_safety = true
lookback_threshold = 16          # Inject safety breakpoint after this many blocks since last cache write

# Cache cost tracking
cache_tracking = true            # Log cache hit/miss/cost per turn
cache_hit_warning = 0.5          # Warn if hit rate drops below this (0.0-1.0)

# Cache stability
normalize_system_prompt = true   # Normalize whitespace/line endings before hashing
sort_tools = true                # Sort tool definitions by name for cache stability

# Extended thinking
[providers.anthropic.thinking]
mode = "adaptive"                # "off" | "adaptive" | "extended"
budget = 0                       # 0 = provider default, or explicit token budget

# Heartbeat keep-warm: if heartbeat.every < cache_ttl, heartbeats keep the cache alive
# (55m heartbeat + 1h cache = cache never expires during active use)

[providers.gemini]
api_key = ""
model = "gemini-3.1-pro"         # Gemini 3.1 Pro (1M+ ctx), Gemini 3 Flash, 3.1 Flash-Lite
max_tokens = 16384
context_window = 1048576         # 1M tokens

[providers.openai]
api_key = ""
model = "gpt-5.5"
fallback_models = ["gpt-5.4", "gpt-5.4-mini"]
max_tokens = 16384
context_window = 128000

[providers.openrouter]
api_key = ""
base_url = "https://openrouter.ai/api/v1"  # OpenAI API-shaped
model = "anthropic/claude-sonnet-4-6"       # provider/model format
max_tokens = 64000
context_window = 1000000
# OpenRouter-specific: cache_control pass-through for Anthropic models (5m only)
# 1h TTL is NOT available via OpenRouter — auto-downgrades to 5m
# No HTTP-Referer or X-OpenRouter-Title headers (anonymization)

[providers.ollama]
base_url = "http://localhost:11434"
model = "llama3.2"
max_tokens = 4096
context_window = 8192

# ─── Embeddings ───
[embeddings]
provider = "openai"
model = "text-embedding-3-small"
api_key = ""               # Falls back to providers.openai.api_key if empty
dimensions = 1536
batch_size = 100           # Max texts per embedding API call

[memory.semantic]
enabled = false
backend = "local"          # "local" first; remote/vector-store backends remain auxiliary
refresh = "manual"         # "manual" | "heartbeat" | duration
sources = ["MEMORY.md", "memory/knowledge.md", "memory/decisions.md"]
include_daily_notes = true
include_questions = false
include_rhizome = false
interactive_top_k = 5
heartbeat_top_k = 12
interactive_max_chars = 4000
heartbeat_max_chars = 12000

# ─── OpenAI Platform Services ───
# These are distinct from OpenAI inference in [providers.openai].
[openai.files]
enabled = false
purpose = "assistants"

[openai.vector_stores]
enabled = false
default_store = ""

These sections are runtime-owned feature gates for OpenAI platform storage. When enabled, they should reuse the configured OpenAI API credentials rather than inventing a second hidden auth path.

Semantic retrieval config should distinguish:

- indexing substrate
- indexed sources
- interactive retrieval limits
- heartbeat/reflection retrieval limits

The turn-time and maintenance retrieval modes should not be forced through one identical set of limits.

[openai.transcription]
enabled = false
model = "whisper-1"

# ─── Governor / Face ───
[governor]
backend = "auto"              # "auto" | "codex" | "native"
native_provider = ""          # Empty lets providers.selection choose from configured providers.

# Runtime failover may still use the native provider chain when Codex fails mid-turn.

[governor.codex]
auth_source = "auto"          # "auto" | "codex_cli" | "aphelion"
auth_path = ""                # Empty = ~/.aphelion/state/codex-auth.json
codex_home = ""               # Empty = CODEX_HOME or ~/.codex
base_url = "https://chatgpt.com/backend-api"
model = "gpt-5.5"
store_responses = true          # Try Codex previous_response_id continuation; auto-fall back to local replay if unsupported.
max_continuations = 3
transport_retries = 1

[face]
backend = "provider"          # "provider" | "floor_fallback" (dedicated floor-to-user fallback serializer)
provider = "anthropic"
model_override = ""
profile = "host"
bootstrap_files = ["IDOLUM.md"]
dynamic_files = ["QUESTIONS-TO-IDOLUM.md"]
persist_floor = true          # Keep governor floor text as sidecar audit data

# ─── Agent ───
[agent]
prompt_root = "~/.aphelion/agent"
exec_root = "~/.aphelion/workspace"
shared_memory_root = "~/.aphelion/agent"
user_workspace_root = "~/.aphelion/state/isolated/workspaces"
user_memory_root = "~/.aphelion/state/isolated/memory"
max_iterations = 50
tool_timeout = 300         # Max seconds per tool execution

# Bootstrap files loaded into system prompt (in order).
# Paths relative to prompt_root. Files that don't exist are silently skipped.
bootstrap_files = [
    "SOUL.md",
    "IDENTITY.md",
    "USER.md",
    "AGENTS.md",
    "TOOLS.md",
    "BOOTSTRAP.md",
]

# Dynamic context files — loaded each turn but placed after cache boundary.
# These change frequently and should NOT be part of the cached prefix.
# MEMORY.md is durable on disk but still dynamic in prompt placement.
dynamic_files = [
    "MEMORY.md",
    "HEARTBEAT.md",
]

# Daily memory notes — auto-resolved to today + yesterday.
# Pattern: memory/YYYY-MM-DD.md in shared_memory_root or user_memory_root.
daily_notes = true
daily_notes_dir = "memory/daily"

# ─── Tools ───
[tools]
# Directory containing external tool manifest JSON files.
# Empty disables external manifest loading.
external_manifest_dir = ""

# ─── Sessions ───
[sessions]
db_path = "~/.aphelion/state/sessions.db"
# Session expiry for admitted Telegram chats and durable child conversations.
idle_expiry = "24h"           # Expire sessions after this much inactivity

# Principal bootstrap:
[principals.telegram]
admin_user_ids = [123456789]
# one or more configured admins may be admitted by Telegram user id

[autonomy]
default_mode = "ask_first"
ceiling = "leased"
allow_live_overrides = true
max_override_duration = "4h"

# Context management thresholds — push close to the provider's actual limit.
# These are in tokens. Compaction kicks in when the assembled prompt exceeds max_context_tokens.
# These are per-provider — resolved at runtime from the active provider's context_window.
# Expressed as ratios of the provider's context_window.
max_context_ratio = 0.75      # Trigger compaction at 75% of context_window. Models degrade near limits (anxiety, hallucination).
compaction_ratio = 0.55       # Compact down to 55% of context_window. Gives ~20% headroom before next compaction.
compaction_strategy = "summarize"  # "summarize" (LLM-assisted) | "truncate" (drop oldest turns)

# Optional group session policy:
[sessions.groups]
scope = "per_user"            # "per_user" (one session per user per group) | "shared" (one session per group)

# Review controls:
[reviews]
enabled = true
digest_every = "30m"
digest_on_idle = true
max_summary_chars = 1200

[sessions.isolation]
global_root = "~/.aphelion/agent"
shared_memory_root = "~/.aphelion/agent"
user_workspace_root = "~/.aphelion/state/isolated/workspaces"
user_memory_root = "~/.aphelion/state/isolated/memory"

[sandbox.profiles.admin]
mode = "trusted"               # "trusted" | "isolated"

[sandbox.profiles.approved_user]
mode = "isolated"
readonly_root = true
writable_paths = ["{user_workspace}", "{user_memory}", "/tmp"]
readonly_paths = ["{global_root}", "{shared_memory_root}"]
hidden_paths = [
    "~/.aphelion/aphelion.toml",
    "~/.ssh",
    "~/.gnupg",
]
network = "deny"               # "deny" | "allowlist"
network_allow = []             # host:port, ip:port, or cidr:port when network="allowlist"

# Isolated `allowlist` requires explicit destinations and a working helper-backed
# host network backend. Run `aphelion sandbox-net check --config <path>` before
# switching a live non-admin or durable profile from `deny`.

# ─── Automation ───
[heartbeat]
enabled = true
every = "30m"
model = "anthropic"           # Can point to a cheaper provider/model for heartbeats
model_override = ""           # e.g. "claude-haiku-3.5" — overrides the provider's default model
active_hours = { start = "08:00", end = "24:00", timezone = "America/New_York" }
target = "last"               # "last" | "none" | specific chat ID

[cron]
jobs = []

# ─── Voice ───
[voice]
provider = "elevenlabs"
mode = "auto"                # off | auto | all
api_key = ""
voice_id = ""
model = "eleven_turbo_v2_5"

# ─── Potential future resource-limit sandbox keys ───
# These keys are not part of the live config schema. The live network allowlist
# surface is the per-profile `network_allow` list above.
[sandbox]
enabled = true
timeout = 300

# Resource limits (cgroups v2)
memory_limit = "512M"
cpu_limit = 1.0              # Number of CPUs
pid_limit = 64
io_weight = 100              # IO priority (1-10000, default 100)

# Filesystem isolation
readonly_root = false        # Mount / as read-only for exec (bind-mount workspace as writable)
writable_paths = []          # Additional writable paths beyond workspace and /tmp
hidden_paths = [             # Paths invisible to exec processes
    "/home/*/.ssh",
    "/home/*/.gnupg",
    "/home/*/.aphelion/aphelion.toml",       # Don't let exec see our config
]

# Network isolation
[sandbox.network]
isolation = "firewall"       # "none" | "full" (blank namespace) | "firewall" (allowlist)
# When isolation = "firewall", only these destinations are reachable.
# Uses helper-owned nftables rules inside a network namespace with a veth pair.
allow = [
    # LLM providers (for sub-agents that need API access)
    "api.anthropic.com:443",
    "generativelanguage.googleapis.com:443",
    "api.openai.com:443",
    # Package managers (for tool exec that installs deps)
    "pypi.org:443",
    "registry.npmjs.org:443",
    "proxy.golang.org:443",
    # Git
    "github.com:443",
    "github.com:22",
]
# Deny takes precedence over allow (for blocking specific IPs within allowed ranges)
deny = []
# DNS: resolve through host by default, or specify a DNS server
dns = "host"                 # "host" (use host's resolver) | "1.1.1.1" | "8.8.8.8"
# Rate limiting: prevent exec from flooding the network
rate_limit = "1mbit"         # tc rate limit on the veth interface
conn_limit = 50              # Max concurrent connections via conntrack

# Process security
[sandbox.security]
# seccomp-bpf: restrict syscalls available to exec processes
seccomp = "moderate"         # "off" | "moderate" (block dangerous syscalls) | "strict" (minimal syscall set)
# Dangerous syscalls blocked in "moderate":
# - ptrace (no debugging other processes)
# - mount/umount (no filesystem changes)
# - reboot, swapon/swapoff, init_module
# - keyctl (no kernel keyring access)
# - bpf (no eBPF programs)

# Capabilities: Linux capabilities to DROP from exec processes
drop_capabilities = [
    "CAP_SYS_ADMIN",
    "CAP_NET_ADMIN",         # Even with firewall mode, exec doesn't get to change rules
    "CAP_SYS_PTRACE",
    "CAP_SYS_RAWIO",
    "CAP_MKNOD",
    "CAP_SYS_MODULE",
    "CAP_DAC_OVERRIDE",
]

# User namespace: run exec as nobody/nogroup inside a user namespace
user_namespace = true
uid_map = "65534"            # Map to nobody
gid_map = "65534"            # Map to nogroup

## Durable Telegram Group Bootstrap

Implemented behavior:

- `[[telegram.durable_groups]]` admits specific Telegram groups as durable children.
- Each admitted group must define a child-local node LLM bootstrap.
- The runtime currently supports:
- `llm_backend = "native"` with `llm_provider = "anthropic" | "openai" | "openrouter" | "gemini" | "ollama"`
  - `llm_backend = "codex"` with child-local Codex auth/home settings
- A durable child must not inherit the parent governor/provider credentials.
- If the child bootstrap is missing or invalid, the durable child should fail rather than silently execute on the parent's LLM credentials.

Current implementation detail:

- same-host Telegram durable groups seed their child bootstrap from `aphelion.toml`
- the durable-agent record persists that bootstrap separately from live policy
- parent-authored live policy may narrow behavior within the bootstrap ceiling, but it does not carry secret-bearing LLM credentials

# ─── Logging ───
[logging]
level = "info"             # debug, info, warn, error
format = "text"            # text (dev) or json (production/journald)
# Log token usage per turn for cost tracking
log_token_usage = true

# ─── HTTP Transport ───
# Fine-grained control over the shared HTTP client.
[http]
max_idle_conns = 10
max_idle_conns_per_host = 5
idle_conn_timeout = "90s"
tls_min_version = "1.2"
# TCP keep-alive for long-lived connections to LLM providers
tcp_keepalive = "30s"
# Response header timeout — how long to wait for the server to start responding
response_header_timeout = "120s"
# Expect-continue timeout — for streaming, we want this fast
expect_continue_timeout = "1s"
# Disable HTTP/2 if it causes issues with a provider
force_http1 = false

# ─── Linux-Specific ───
[linux]
# cgroups v2 root for tool sandboxing. Must be writable by the daemon user.
cgroup_root = "/sys/fs/cgroup/aphelion"
# Use memfd for credential sealing
use_memfd = true
# Use pidfd for child process management
use_pidfd = true
# Namespace features (requires CAP_SYS_ADMIN or unprivileged user namespaces)
user_namespaces = true       # Required for sandbox.security.user_namespace
network_namespaces = true    # Required for sandbox.network.isolation != "none"
```

## String Anonymization

### The Problem

Claude's subscription terms restrict third-party tool use. Detection likely keys on identifiable strings: project names in system prompts, distinctive user-agent headers, characteristic message structures, metadata fields.

### The Solution

**Nothing identifies the harness unless you choose to.**

1. **HTTP User-Agent**: Configurable. Default is Go's standard `Go-http-client/2.0` — indistinguishable from any Go program.

2. **No project name in API traffic.** The word "aphelion" (or any project name) never appears in:
   - HTTP headers sent to providers
   - System prompts
   - Tool definitions
   - Message content injected by the runtime

3. **System prompt markers**: No `<!-- CACHE_BOUNDARY -->` or equivalent marker strings. Cache boundaries are managed by message structure and the Anthropic `cache_control` API field, not inline text markers.

4. **Tool names**: Generic. `exec`, `read_file`, `write_file`, `web_fetch`, `memory_search`. Not branded.

5. **Error messages**: "Tool execution failed" not "Aphelion tool execution failed".

6. **No telemetry.** No analytics. No phone-home. No crash reporting.

7. **HTTP headers**: Only the minimum required headers. No `X-Aphelion-*` headers. No custom trace IDs in provider requests.

8. **Anthropic API version**: Standard `anthropic-version: 2023-06-01` header. Nothing extra.

### What a Provider Sees

```http
POST /v1/messages HTTP/2
Host: api.anthropic.com
anthropic-version: 2023-06-01
x-api-key: sk-ant-...
content-type: application/json
user-agent: Go-http-client/2.0

{
  "model": "claude-sonnet-4-6",
  "max_tokens": 16384,
  "cache_control": {"type": "ephemeral", "ttl": "1h"},
  "system": [...],
  "messages": [...]
}
```

Indistinguishable from any Go application using the Anthropic API directly.

## Credential Management

### At Rest

Credentials can be stored in:
1. **Config file** (`config.toml`) — simple, appropriate for single-user machines
2. **Environment variables** — `ANTHROPIC_API_KEY`, `TELEGRAM_BOT_TOKEN`, etc.
3. **Env file** — `.env` in the config directory, loaded at startup

Priority: env vars > env file > config file.

### In Memory (memfd)

At startup, credentials are:
1. Read from their source
2. Written to a `memfd_create(2)` anonymous memory fd with `MFD_CLOEXEC`
3. Original env vars are overwritten with zeros via `os.Unsetenv()` + explicit zeroing
4. The memfd is `MFD_CLOEXEC` — invisible to child processes spawned via `exec`

```go
func sealCredential(key string, value []byte) (*MemCredential, error) {
    fd, err := unix.MemfdCreate("cred-"+key, unix.MFD_CLOEXEC)
    if err != nil {
        return nil, err
    }
    f := os.NewFile(uintptr(fd), key)
    f.Write(value)
    // Optionally: unix.Fmemfd_seal(fd, F_SEAL_WRITE|F_SEAL_SHRINK|F_SEAL_GROW)
    // to make the fd immutable after writing
    f.Seek(0, 0)
    for i := range value {
        value[i] = 0
    }
    return &MemCredential{file: f}, nil
}
```

The optional `F_SEAL_*` flags make the memfd immutable after initial write — prevents any code path from accidentally modifying credentials in-memory.

### Credential Injection

Provider adapters read credentials from the sealed memfd per-request:

```go
func (c *MemCredential) Read() ([]byte, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    c.file.Seek(0, 0)
    return io.ReadAll(c.file)
}
```

Thread-safe via `RWMutex`. Multiple goroutines (concurrent turns for different sessions) can read credentials simultaneously.

## Config Loading

```go
func Load(path string) (*Config, error) {
    // 1. Read TOML file via BurntSushi/toml
    // 2. Apply env var overrides (APHELION_* prefix, nested via __)
    //    e.g. APHELION_PROVIDERS__ANTHROPIC__API_KEY
    // 3. Load .env file if present (simple KEY=VALUE parsing, no shell expansion)
    // 4. Expand ~ in paths to $HOME
    // 5. Validate:
    //    - Required: telegram.bot_token, at least one provider with api_key
    //    - providers.selection must be auto|manual, and the effective provider chain must reference configured providers
    //    - cache_ttl must be "5m" or "1h"
    //    - max_context_tokens < provider's context_window
    //    - logging.level must be debug|info|warn|error
    //    - active_hours.start < active_hours.end
    // 6. Seal credentials into memfd
    // 7. Return Config
}
```

### Recovery watchdog

```toml
[recovery.watchdog]
enabled = true
stale_turn_threshold = "3m"
stale_turn_limit = 8
restart_cooldown = "30m"
max_restart_attempts = 1
```

The stale-turn watchdog is a recovery controller, not a generic process supervisor.
It may request a process restart only after it has written durable watchdog facts
and successfully persisted interrupted turn state. `restart_cooldown` is the
rolling window for `max_restart_attempts`; together they prevent repeated
watchdog-triggered restart loops while still allowing a later retry when the
window clears.

### Hot Reload

Not supported in v1. Restart is cheap (<100ms cold start).

## Staging

- **Required for a runnable daemon**: identity strings, active channel credentials, one working provider, session storage, config-assigned Telegram principals, workspace prompt files, and the agent execution limits that bound a turn.
- **Required for the governor/face architecture**: a governor backend selection policy, Codex-friendly governor ownership boundaries, and an explicit face rendering slot even if the first implementation is thin.
- **Required for DM admission and authority**: a config-owned principal model for Telegram DMs, at least one admin principal, and clear config ownership for later authority roles and isolated roots.
- **Required for a hardened local system tool**: sandbox controls, HTTP transport tuning, failover policy, and credential sealing.
- **Reserved architectural surface**: any future group, durable-agent, provider, embedding, voice, cron, or deeper Linux controls may be described here only as future design notes until they are accepted by `config.Config` and `config.example.toml`.

## Decisions

- **TOML.** Human-friendly, comment-friendly, Go ecosystem default.
- **Single file.** No `config.d/`, no merge logic. One file, one truth.
- **Live schema before breadth.** Config breadth is not a promise. Live keys must be accepted, validated, projected, and tested; future keys must warn if used.
- **Admission and authority are first-class config concerns.** Config is the source of truth for Telegram admin principals and the roots/policies used by scoped child principals.
- **Governor and face are separate config concerns.** Decision-making and presentation should be independently configurable even if one implementation path is initially minimal.
- **memfd with F_SEAL for credentials.** Immutable in-memory secrets. Defense in depth.
- **No hot reload.** Simplicity. Single-binary restart is fast.
- **Bootstrap vs dynamic files.** Bootstrap files (SOUL.md, IDENTITY.md, etc.) are stable and go in the cached prefix. Dynamic files (MEMORY.md, HEARTBEAT.md, daily notes) change often and go after the cache boundary. This maximizes cache hit rate.
- **Provider context windows are explicit.** No guessing. The config states the model's actual context window, and the session manager uses it for compaction decisions.
- **HTTP transport is configurable.** TCP keepalive, TLS version, timeouts — all exposed. For a daemon that maintains long-lived connections to 2-3 providers, these matter.

## Tests

### config loading

- **TestLoadMinimal**: TOML with only `[telegram]` bot_token and `[providers.anthropic]` api_key → loads without error, defaults are correct (workspace path, max_iterations=50, etc.).
- **TestLoadFull**: TOML with every field populated → all fields parsed correctly.
- **TestMissingRequired**: TOML without telegram.bot_token → returns descriptive error.
- **TestExpandTilde**: Paths like `~/workspace` expand to absolute paths.
- **TestEnvOverride**: Set `APHELION_PROVIDERS__ANTHROPIC__API_KEY=sk-test` → overrides config file value.
- **TestEnvFile**: Write `.env` file with `ANTHROPIC_API_KEY=sk-env` → loaded when config file value is empty.
- **TestPrecedence**: Config file has key A, env file has key B, env var has key C → env var wins.

### credential sealing (memfd)

- **TestSealCredential**: Seal a string → read it back → matches original.
- **TestSealZerosOriginal**: Seal a byte slice → original slice is all zeros after sealing.
- **TestSealedNotInEnviron**: Seal from env var → `os.Getenv()` returns empty after sealing.
- **TestMemfdCloexec**: Sealed fd has CLOEXEC flag set (via `fcntl` check).
- **TestMemfdSeal**: After sealing with F_SEAL_WRITE, writing to fd returns error.
- **TestConcurrentRead**: 100 goroutines read credential simultaneously → all get correct value, no races.

### anonymization

- **TestDefaultUserAgent**: Default config → User-Agent header is Go's default, not "aphelion".
- **TestNoProjectNameInHeaders**: Build an HTTP request with default config → no header contains "aphelion".
- **TestCustomUserAgent**: Set `identity.user_agent = "MyBot/1.0"` → User-Agent header matches.
- **TestSystemPromptNoMarkers**: Assemble a system prompt from bootstrap files → no cache boundary markers, no project name strings.

### sandbox & security

- **TestCgroupCreation**: With sandbox enabled, exec creates cgroup under configured root with correct memory/cpu/pid limits.
- **TestNetworkFirewall**: With `isolation = "firewall"`, exec can reach `api.anthropic.com:443` but not `evil.com:443`.
- **TestNetworkFull**: With `isolation = "full"`, exec cannot reach any network address.
- **TestNetworkNone**: With `isolation = "none"`, exec has full network access.
- **TestHiddenPaths**: With `hidden_paths` set, exec cannot read `~/.aphelion/aphelion.toml`.
- **TestSeccompModerate**: With `seccomp = "moderate"`, exec cannot call `ptrace()` or `mount()`.
- **TestUserNamespace**: With `user_namespace = true`, exec runs as uid 65534 (nobody).
- **TestDropCapabilities**: Exec process does not have CAP_SYS_ADMIN or CAP_NET_ADMIN.
- **TestResourceLimits**: Exec process that exceeds memory_limit is OOM-killed by cgroup.

### validation

- **TestInvalidProvider**: `providers.default = "nonexistent"` → error.
- **TestInvalidCacheTTL**: `providers.anthropic.cache_ttl = "10m"` → error (only "5m" or "1h" allowed).
- **TestInvalidLogLevel**: `logging.level = "verbose"` → error.
- **TestContextExceedsWindow**: `sessions.max_context_tokens = 250000` with `providers.anthropic.context_window = 200000` → error.
- **TestActiveHoursInvalid**: `start = "22:00", end = "08:00"` → error (or handle wrap-around — decide in implementation).
