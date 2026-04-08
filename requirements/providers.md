# Providers — LLM Adapters, Caching & Failover

## Overview

Aphelion talks to LLM providers via direct HTTP. No SDKs. We own every byte on the wire — headers, user-agent, cache control markers. Each provider is an implementation of a single `Provider` interface.

## Provider Interface

```go
type Provider interface {
    // Complete sends messages to the LLM and returns a response.
    // Streaming is handled internally; the response is fully assembled.
    Complete(ctx context.Context, req *Request) (*Response, error)

    // Stream sends messages and calls the callback for each chunk.
    Stream(ctx context.Context, req *Request, cb StreamCallback) (*Response, error)

    // Name returns the provider identifier (e.g., "anthropic", "gemini").
    Name() string

    // Model returns the active model string.
    Model() string

    // ContextWindow returns the model's context window in tokens.
    ContextWindow() int
}

type Request struct {
    SystemPrompt   []ContentBlock   // System prompt blocks (stable + dynamic)
    Messages       []Message        // Conversation history + new user message
    Tools          []ToolDef        // Tool definitions
    MaxTokens      int
    Temperature    *float64         // nil = provider default
    StopSequences  []string
    // Caching
    CacheControl   *CacheControl    // Top-level automatic caching
    // Thinking
    ThinkingMode   string           // "off", "adaptive", "extended"
    ThinkingBudget int              // Max thinking tokens (0 = provider default)
}

type CacheControl struct {
    Type string // "ephemeral"
    TTL  string // "5m" or "1h"
}

type StreamCallback func(chunk StreamChunk)

type StreamChunk struct {
    Type    string // "text", "tool_call", "thinking", "usage"
    Text    string
    ToolCall *ToolCall
    Usage   *TokenUsage
}
```

## Supported Providers

### Anthropic (Messages API)

**Endpoint**: `https://api.anthropic.com/v1/messages`

**Current models**:
- `claude-opus-4-6` — 1M context, 128K max output, $5/$25 per MTok
- `claude-sonnet-4-6` — 1M context, 64K max output, $3/$15 per MTok
- `claude-haiku-4-5` — 200K context, 64K max output, $1/$5 per MTok

**Headers** (minimal, anonymous):
```
anthropic-version: 2023-06-01
x-api-key: <from memfd>
content-type: application/json
```

No custom headers. No user-agent override (Go default). No telemetry.

**Streaming**: SSE via `"stream": true`. Events: `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`.

**Extended thinking**: `"thinking": {"type": "enabled", "budget_tokens": N}` in request body.

### Gemini (Google AI)

**Endpoint**: `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent` (non-streaming) or `:streamGenerateContent` (streaming)

**Current models**:
- `gemini-3.1-pro` — 1M+ context, frontier reasoning
- `gemini-3-flash` — 1M context, fast
- `gemini-3.1-flash-lite` — efficient

**Auth**: API key as query param `?key=<key>` or `x-goog-api-key` header.

**Streaming**: SSE, chunked JSON responses.

### OpenAI (Chat Completions)

**Endpoint**: `https://api.openai.com/v1/chat/completions`

**Current models**:
- `gpt-5.4` — flagship reasoning + coding
- `gpt-5.4-mini` — lower latency/cost
- `gpt-5.4-nano` — fastest/cheapest

**Auth**: `Authorization: Bearer <key>`

**Caching**: Automatic prefix caching on supported models. No explicit markers needed. Reports `prompt_tokens_details.cached_tokens` in usage.

### Ollama (local)

**Endpoint**: `http://localhost:11434/api/chat`

**Models**: whatever's pulled locally (`llama3.2`, etc.)

**No auth, no caching, no streaming SSE** (Ollama uses newline-delimited JSON streaming).

---

## Prompt Caching — The Full Strategy

This is the most cost-critical part of Aphelion. Done right, caching reduces Anthropic input costs by ~90% on multi-turn sessions.

### Cache Architecture

```
┌─────────────────────────────────────────────┐
│ CACHED PREFIX (stable across turns)          │
│                                              │
│  ┌─ tools[] ─────────────────────────────┐  │
│  │ Tool definitions (sorted, stable)      │  │
│  │ cache_control: {ephemeral, ttl: 1h}   │  │ ← Explicit breakpoint #1
│  └────────────────────────────────────────┘  │
│                                              │
│  ┌─ system[] ────────────────────────────┐  │
│  │ Bootstrap files (SOUL, IDENTITY, etc)  │  │
│  │ cache_control: {ephemeral, ttl: 1h}   │  │ ← Explicit breakpoint #2
│  └────────────────────────────────────────┘  │
│                                              │
├─────────────────────────────────────────────┤
│ DYNAMIC SUFFIX (changes each turn)           │
│                                              │
│  ┌─ system[] (continued) ────────────────┐  │
│  │ MEMORY.md, HEARTBEAT.md, daily notes   │  │
│  │ Timestamp, runtime metadata            │  │
│  └────────────────────────────────────────┘  │
│                                              │
│  ┌─ messages[] ──────────────────────────┐  │
│  │ Conversation history                   │  │
│  │ (auto cache_control advances here)     │  │ ← Automatic breakpoint (top-level)
│  └────────────────────────────────────────┘  │
│                                              │
│  ┌─ New user message ────────────────────┐  │
│  └────────────────────────────────────────┘  │
└─────────────────────────────────────────────┘
```

### Hybrid Strategy (default)

Uses 3 of Anthropic's 4 cache breakpoint slots:

1. **Explicit #1**: On the last tool definition block. Tools rarely change.
2. **Explicit #2**: On the last stable system prompt block (end of bootstrap files). Changes only when you edit SOUL.md etc.
3. **Automatic**: Top-level `cache_control` on the request body. Auto-advances to the last cacheable block (end of conversation history) each turn.

Slot 4 is reserved for the **block count safety valve** (see below).

### Caching Techniques

#### 1. Deterministic tool ordering
Tool definitions are sorted by name before every request. No churn from map iteration order or registration order.

#### 2. System prompt fingerprinting
Before comparing system prompts across turns, normalize:
- Trailing whitespace
- Line endings (CRLF → LF)
- Multiple blank lines → single blank line
- Trailing newline

If the normalized fingerprint matches the previous turn, reuse the exact byte sequence (don't re-render). This prevents cache-busting from trivial whitespace differences.

#### 3. Cache-TTL pruning
After the cache TTL expires (the next request will re-cache), prune old tool results:
- **Soft-trim**: Results older than N turns get head+tail with `...` in the middle.
- **Hard-clear**: Results older than 2N turns get replaced with `[tool result trimmed]`.

This shrinks the re-cache write, directly reducing cost. The pruning is in-memory only — the full session transcript is preserved on disk.

#### 4. Heartbeat keep-warm
With `cache_ttl = "1h"` and `heartbeat.every = "55m"`, every heartbeat turn refreshes the cache before it expires. The cache never goes cold during active use. Cost: one heartbeat turn of cache-read tokens every 55 minutes, vs a full cache-write if it expires.

#### 5. 20-block lookback safety valve
Anthropic's cache lookback window is 20 blocks. In long conversations with many tool calls, blocks accumulate fast. If we detect that blocks since the last cache write are approaching 20:
- Inject explicit breakpoint #4 at the current position before sending
- This ensures the lookback window can find the previous cache entry

#### 6. Pre-compaction cache awareness
Before compacting a conversation, check:
- Will the compacted result still exceed `min_cache_tokens`? If not, the system prompt won't be cacheable after compaction. Pad with stable context or adjust the compaction target.
- After compaction, the cache prefix is invalidated. The next request will be a full cache write. Schedule compaction when a cache write is already expected (e.g., right after TTL expiry).

#### 7. Cache cost tracking
Every response includes `cache_creation_input_tokens` and `cache_read_input_tokens`. Track:
- Running cache hit rate: `read / (read + write)`
- Cost savings: `(read_tokens * base_price - read_tokens * cache_read_price)`
- Log per-turn and per-session cache stats via slog

If hit rate drops below a configurable threshold, log a warning with diagnosis hints.

### Config Surface for Caching

```toml
[providers.anthropic]
# ... (api_key, model, etc. from config.md)

# Cache strategy
cache_strategy = "hybrid"        # "auto" | "explicit" | "hybrid" | "off"
cache_ttl = "1h"                 # "5m" | "1h"

# Cache-TTL pruning
cache_ttl_pruning = true
pruning_soft_age = 10            # Soft-trim tool results older than N turns
pruning_hard_age = 20            # Hard-clear tool results older than N turns

# Block lookback safety
lookback_safety = true           # Auto-inject breakpoint when approaching 20-block window
lookback_threshold = 16          # Inject safety breakpoint after this many blocks since last write

# Cache cost tracking
cache_tracking = true            # Log cache hit/miss/cost per turn
cache_hit_warning = 0.5          # Warn if hit rate drops below this (0.0 - 1.0)

# System prompt stability
normalize_system_prompt = true   # Normalize whitespace/line endings before hashing
sort_tools = true                # Sort tool definitions by name for cache stability

# Min cacheable tokens (validated at startup against model)
min_cache_tokens = 2048          # Below this, system prompt won't be cached

[providers.anthropic.thinking]
mode = "adaptive"                # "off" | "adaptive" | "extended"
budget = 0                       # 0 = provider default, or explicit token budget
```

### Per-Provider Caching Notes

| Provider | Caching | Our Strategy |
|----------|---------|-------------|
| **Anthropic** | Explicit + automatic breakpoints, 5m/1h TTL, usage counters | Full hybrid strategy with all 7 techniques |
| **OpenAI** | Automatic prefix caching, no explicit control | Sort tools, normalize prompt. Track `cached_tokens` from usage. No breakpoints to manage. |
| **Gemini** | `cachedContents` resource (server-side), separate from request | Create + refresh cached content handle for system prompt. Less granular than Anthropic. |
| **Ollama** | None | No caching. Local latency is low enough that it doesn't matter. |

---

## Failover

When the default provider errors persistently (3 retries exhausted), try the next provider in the `failover` chain.

```go
func (f *FailoverChain) Complete(ctx context.Context, req *Request) (*Response, error) {
    for _, provider := range f.providers {
        resp, err := provider.Complete(ctx, req)
        if err == nil {
            return resp, nil
        }
        if !isRetryable(err) {
            return nil, err // 4xx errors don't failover
        }
        slog.Warn("provider failed, trying next",
            "provider", provider.Name(),
            "error", err,
            "next", f.nextName(provider))
    }
    return nil, fmt.Errorf("all providers exhausted")
}
```

Failover rules:
- **Retry within provider first**: 429/500/502/503 → exponential backoff, max 3 retries.
- **Then failover**: If all retries fail, try next provider in chain.
- **Don't failover on 4xx** (except 429): bad request is our bug, not provider downtime.
- **Translate messages**: Different providers have different message formats. The failover chain handles translation transparently.
- **Log provider switches**: Every failover is logged with the error that triggered it.
- **Restore primary on next turn**: After failover, the next turn tries the primary provider again.

### Config

```toml
[providers]
default = "anthropic"
failover = ["gemini", "ollama"]

[providers.failover]
max_retries = 3                  # Per-provider retries before failover
retry_backoff_base = 1.0         # Seconds, doubled each retry
retry_backoff_max = 30.0         # Cap on backoff delay
restore_primary = true           # Try primary again on next turn after failover
```

---

## SSE Parsing

All streaming providers use SSE (except Ollama, which uses newline-delimited JSON). Our SSE parser is provider-agnostic:

```go
// internal/sse.go
type Event struct {
    Type string // "event" field, empty if not present
    Data string // "data" field (may be multi-line, joined with \n)
    ID   string // "id" field
}

func ParseSSE(r io.Reader) <-chan Event {
    // Read line by line
    // Accumulate data lines
    // Emit Event on blank line
    // Handle :comments (ignore)
    // Handle retry: (ignore for now)
}
```

---

## Token Counting

We need approximate token counts for:
- Context window management (when to trigger compaction)
- Cache strategy decisions (is system prompt above min_cache_tokens?)
- Cost tracking

Strategy: **character-based estimation** for decisions, **provider-reported usage** for accounting.

```go
// Rough estimation: 1 token ≈ 4 characters for English text.
// This is used for pre-flight context checks, not billing.
func EstimateTokens(text string) int {
    return (len(text) + 3) / 4
}
```

Actual token usage comes from the provider's response (`usage.input_tokens`, `usage.output_tokens`, etc.) and is logged per-turn.

### Config

```toml
[providers]
# Token estimation
chars_per_token = 4              # Rough estimate for pre-flight checks
# If you need exact counts, we could add tiktoken-go but it's a heavy dep
```

---

## Tests

### Provider interface

- **TestAnthropicComplete**: Mock HTTP server returns valid Anthropic response → parsed correctly into Response.
- **TestAnthropicStream**: Mock SSE stream → StreamCallback receives correct chunks in order.
- **TestAnthropicCacheHeaders**: With `cache_strategy = "hybrid"`, request includes correct `cache_control` on tools, system, and top-level.
- **TestAnthropicCacheTTL**: `cache_ttl = "1h"` → `cache_control` includes `"ttl": "1h"`.
- **TestAnthropicThinking**: `thinking.mode = "extended"` → request includes `thinking` field.
- **TestGeminiComplete**: Mock HTTP server returns valid Gemini response → parsed correctly.
- **TestOpenAIComplete**: Mock HTTP server returns valid OpenAI response → parsed correctly.
- **TestOllamaComplete**: Mock HTTP server returns Ollama NDJSON → parsed correctly.

### Caching

- **TestDeterministicToolOrder**: Register tools in random order → request always has them sorted by name.
- **TestSystemPromptFingerprint**: Two prompts differing only in whitespace → same fingerprint.
- **TestSystemPromptChanged**: Prompts with different content → different fingerprint → cache-busting detected.
- **TestCacheTTLPruning**: After N turns, old tool results are soft-trimmed/hard-cleared in assembled messages.
- **TestLookbackSafety**: After 16 blocks since last write, explicit breakpoint #4 is injected.
- **TestPreCompactionCacheCheck**: Compaction that would drop below min_cache_tokens → warning logged.
- **TestCacheTracking**: Mock response with cache counters → hit rate computed correctly.
- **TestCacheHitWarning**: Hit rate below threshold → warning logged.

### Failover

- **TestFailoverToSecondary**: Primary returns 500 on all retries → secondary is tried → succeeds.
- **TestNoFailoverOn400**: Primary returns 400 → error returned immediately, no failover.
- **TestFailoverExhausted**: All providers fail → descriptive error returned.
- **TestRestorePrimary**: After failover, next turn starts with primary again.
- **TestRetryBackoff**: 429 → verify exponential backoff timing (within tolerance).

### SSE

- **TestSSEBasic**: Standard SSE events → parsed correctly.
- **TestSSEMultilineData**: Multi-line `data:` fields → joined with `\n`.
- **TestSSEComments**: Lines starting with `:` → ignored.
- **TestSSEEmptyLines**: Multiple blank lines → single event boundary.
- **TestSSENoEvent**: Events without `event:` field → Type is empty string.

### Token estimation

- **TestEstimateTokens**: Known strings → estimates within 20% of tiktoken ground truth.
