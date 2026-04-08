# Config — Configuration & String Anonymization

## Overview

Aphelion's config is a single TOML file that controls the entire runtime. Every identifiable string — project name, user-agent headers, system prompt markers — is either configurable or absent. The goal: no provider can fingerprint the harness from the traffic it sees.

## Config File

Default location: `~/.config/aphelion/config.toml`

Override via `APHELION_CONFIG` env var or `--config` flag.

```toml
# ─── Identity ───
# These strings appear in HTTP headers.
# Set them to whatever you want, or leave blank.
[identity]
user_agent = ""          # HTTP User-Agent. Empty = Go default ("Go-http-client/2.0")
project_name = ""        # Never sent to providers. Used only in local logs.

# ─── Telegram ───
[telegram]
bot_token = ""
allowed_chats = []       # Chat IDs allowed to interact. Empty = allow all.
poll_timeout = 30        # Long-poll timeout in seconds
# Formatting
max_message_length = 4096  # Telegram's limit; auto-split longer messages
parse_mode = "MarkdownV2" # Default parse mode

# ─── Providers ───
[providers]
default = "anthropic"
failover = ["gemini", "ollama"]

[providers.anthropic]
api_key = ""
model = "claude-sonnet-4-6"
max_tokens = 16384
context_window = 200000    # Model's actual context window
cache_ttl = "1h"           # "5m" (1.25x write cost) or "1h" (2x write cost, but 10x cheaper reads amortized over long sessions)
cache_strategy = "auto"    # "auto" (top-level cache_control) | "explicit" (manual breakpoints on system + last 3)
# Anthropic-specific headers
anthropic_version = "2023-06-01"
# Minimum cacheable tokens per model (for validation):
# Opus 4.6/4.5: 4096, Sonnet 4.6: 2048, Sonnet 4.5/4: 1024, Haiku 4.5: 4096
min_cache_tokens = 4096

[providers.gemini]
api_key = ""
model = "gemini-2.5-pro"
max_tokens = 16384
context_window = 1048576   # Gemini's 1M context

[providers.openai]
api_key = ""
model = "gpt-4.1"
max_tokens = 16384
context_window = 128000

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

# ─── Agent ───
[agent]
workspace = "~/.config/aphelion/workspace"
max_iterations = 50
tool_timeout = 300         # Max seconds per tool execution

# Bootstrap files loaded into system prompt (in order).
# Paths relative to workspace. Files that don't exist are silently skipped.
bootstrap_files = [
    "SOUL.md",
    "IDENTITY.md",
    "USER.md",
    "AGENTS.md",
    "TOOLS.md",
]

# Dynamic context files — loaded each turn but placed after cache boundary.
# These change frequently and should NOT be part of the cached prefix.
dynamic_files = [
    "MEMORY.md",
    "HEARTBEAT.md",
]

# Daily memory notes — auto-resolved to today + yesterday.
# Pattern: memory/YYYY-MM-DD.md in workspace.
daily_notes = true
daily_notes_dir = "memory"

# ─── Sessions ───
[sessions]
db_path = "~/.config/aphelion/sessions.db"
# Context management thresholds — push close to the provider's actual limit.
# These are in tokens. Compaction kicks in when the assembled prompt exceeds max_context_tokens.
max_context_tokens = 190000   # ~95% of Anthropic's 200K. Leave headroom for response.
compaction_target = 140000    # Compact down to this. Aggressive enough to avoid re-compacting soon.
compaction_strategy = "summarize"  # "summarize" (LLM-assisted) | "truncate" (drop oldest turns)
# Session expiry
idle_expiry = "24h"           # Expire sessions after this much inactivity

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
api_key = ""
voice_id = ""
model = "eleven_turbo_v2_5"

# ─── Exec Sandbox ───
[sandbox]
enabled = true
memory_limit = "512M"
cpu_limit = 1.0
pid_limit = 64
timeout = 300
# Network isolation: use unshare(CLONE_NEWNET) to give exec a blank network namespace
# unless the tool explicitly requests network (e.g., web_fetch runs without sandbox net isolation)
network_isolation = true

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
# seccomp-bpf profile for tool exec (future)
# seccomp_profile = ""
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
    //    - providers.default must reference a configured provider
    //    - cache_ttl must be "5m" or "1h"
    //    - max_context_tokens < provider's context_window
    //    - logging.level must be debug|info|warn|error
    //    - active_hours.start < active_hours.end
    // 6. Seal credentials into memfd
    // 7. Return Config
}
```

### Hot Reload

Not supported in v1. Restart is cheap (<100ms cold start).

## Decisions

- **TOML.** Human-friendly, comment-friendly, Go ecosystem default.
- **Single file.** No `config.d/`, no merge logic. One file, one truth.
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

### validation

- **TestInvalidProvider**: `providers.default = "nonexistent"` → error.
- **TestInvalidCacheTTL**: `providers.anthropic.cache_ttl = "10m"` → error (only "5m" or "1h" allowed).
- **TestInvalidLogLevel**: `logging.level = "verbose"` → error.
- **TestContextExceedsWindow**: `sessions.max_context_tokens = 250000` with `providers.anthropic.context_window = 200000` → error.
- **TestActiveHoursInvalid**: `start = "22:00", end = "08:00"` → error (or handle wrap-around — decide in implementation).
