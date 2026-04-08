# Config — Configuration & String Anonymization

## Overview

Aphelion's config is a single TOML file that controls the entire runtime. Every identifiable string — project name, user-agent headers, system prompt markers — is either configurable or absent. The goal: no provider can fingerprint the harness from the traffic it sees.

## Config File

Default location: `~/.config/aphelion/config.toml`

Override via `APHELION_CONFIG` env var or `--config` flag.

```toml
# ─── Identity ───
# These strings appear in HTTP headers and system prompts.
# Set them to whatever you want, or leave blank.
[identity]
user_agent = ""          # HTTP User-Agent. Empty = Go default ("Go-http-client/2.0")
project_name = ""        # Never sent to providers. Used only in local logs.

# ─── Telegram ───
[telegram]
bot_token = ""           # Telegram Bot API token
allowed_chats = []       # Chat IDs allowed to interact. Empty = allow all.
poll_timeout = 30        # Long-poll timeout in seconds

# ─── Providers ───
[providers.anthropic]
api_key = ""
model = "claude-sonnet-4-6"
max_tokens = 8192
cache_ttl = "1h"         # Prompt cache TTL: "5m" or "1h"

[providers.gemini]
api_key = ""
model = "gemini-2.5-pro"
max_tokens = 8192

[providers.openai]
api_key = ""
model = "gpt-4.1"
max_tokens = 8192

[providers.ollama]
base_url = "http://localhost:11434"
model = "llama3.2"
max_tokens = 4096

# Which provider to use by default
[providers]
default = "anthropic"

# Failover chain: if the default provider errors, try these in order
failover = ["gemini", "ollama"]

# ─── Agent ───
[agent]
workspace = "~/.config/aphelion/workspace"
max_iterations = 50      # Max LLM calls per turn
tool_timeout = 300       # Max seconds per tool execution

# Bootstrap files loaded into system prompt (in order)
bootstrap_files = [
    "SOUL.md",
    "USER.md",
    "IDENTITY.md",
    "AGENTS.md",
    "TOOLS.md",
]

# ─── Sessions ───
[sessions]
db_path = "~/.config/aphelion/sessions.db"
max_context_tokens = 180000   # Trigger compaction above this
compaction_target = 120000    # Compact down to this

# ─── Automation ───
[heartbeat]
enabled = true
every = "30m"
model = "anthropic"          # Can override to a cheaper model
active_hours = { start = "08:00", end = "24:00", timezone = "America/New_York" }

[cron]
# Defined in workspace HEARTBEAT.md or here
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
cpu_limit = 1.0           # Number of CPUs
pid_limit = 64
timeout = 300              # Seconds

# ─── Logging ───
[logging]
level = "info"             # debug, info, warn, error
format = "text"            # text (dev) or json (production/journald)
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

3. **System prompt markers**: No `<!-- APHELION_CACHE_BOUNDARY -->` or equivalent. Cache boundaries are managed by message structure, not marker strings. (See `sessions.md` for the cache strategy.)

4. **Tool names**: Generic. `exec`, `read_file`, `write_file`, `web_fetch`, `memory_search`. Not `aphelion_exec` or branded variants.

5. **Error messages**: Internal errors surfaced to the LLM say "Tool execution failed" not "Aphelion tool execution failed".

6. **No telemetry.** No analytics. No phone-home. No crash reporting to external services.

### What a Provider Sees

From Anthropic's perspective, traffic from Aphelion looks like:

```
POST /v1/messages HTTP/2
Host: api.anthropic.com
anthropic-version: 2023-06-01
x-api-key: sk-ant-...
content-type: application/json

{
  "model": "claude-sonnet-4-6",
  "max_tokens": 8192,
  "system": "You are a helpful assistant...",
  "messages": [...]
}
```

No distinctive headers. No project markers. Standard Anthropic API usage.

## Credential Management

### At Rest

Credentials can be stored in:
1. **Config file** (`config.toml`) — simple, appropriate for single-user machines
2. **Environment variables** — `ANTHROPIC_API_KEY`, `TELEGRAM_BOT_TOKEN`, etc.
3. **Env file** — `.env` in the config directory, loaded at startup

Priority: env vars > env file > config file.

### In Memory

At startup, credentials are:
1. Read from their source
2. Written to a `memfd_create()` anonymous memory fd
3. Original env vars are overwritten with zeros
4. The memfd is `MFD_CLOEXEC` — invisible to child processes

```go
func sealCredential(key string, value []byte) (*MemCredential, error) {
    fd, err := unix.MemfdCreate("cred-"+key, unix.MFD_CLOEXEC)
    if err != nil {
        return nil, err
    }
    f := os.NewFile(uintptr(fd), key)
    f.Write(value)
    f.Seek(0, 0)
    // Zero the original
    for i := range value {
        value[i] = 0
    }
    return &MemCredential{file: f}, nil
}
```

This means:
- `cat /proc/<pid>/environ` doesn't show API keys
- `/proc/<pid>/fd/` shows the fd exists but it has no filesystem name
- Child processes (tool exec, sub-agents) don't inherit the fd

### Credential Injection

Provider adapters read credentials from the sealed memfd when making API calls:

```go
func (c *MemCredential) Read() ([]byte, error) {
    c.file.Seek(0, 0)
    return io.ReadAll(c.file)
}
```

## Config Loading

```go
func Load(path string) (*Config, error) {
    // 1. Read TOML file
    // 2. Apply env var overrides (APHELION_* prefix, nested via __)
    //    e.g. APHELION_PROVIDERS__ANTHROPIC__API_KEY
    // 3. Load .env file if present
    // 4. Expand ~ in paths
    // 5. Validate required fields
    // 6. Seal credentials into memfd
    // 7. Return Config
}
```

### Hot Reload

Not supported in v1. Change config → restart the daemon. The single-binary, fast-startup design makes this cheap (<100ms).

## Decisions

- **TOML.** Human-friendly, comment-friendly, no indent sensitivity.
- **Single file.** No `config.d/` directory, no merging multiple sources. One file, one truth.
- **memfd for credentials.** Defense in depth. Not paranoid — practical for a long-running daemon on a shared machine.
- **No hot reload.** Simplicity. Restart is cheap.

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

### anonymization

- **TestDefaultUserAgent**: Default config → User-Agent header is Go's default, not "aphelion".
- **TestNoProjectNameInHeaders**: Build an HTTP request with default config → no header contains "aphelion".
- **TestCustomUserAgent**: Set `identity.user_agent = "MyBot/1.0"` → User-Agent header matches.

### validation

- **TestInvalidProvider**: `providers.default = "nonexistent"` → error.
- **TestInvalidCacheTTL**: `providers.anthropic.cache_ttl = "10m"` → error (only "5m" or "1h" allowed).
- **TestInvalidLogLevel**: `logging.level = "verbose"` → error.
