# Providers — Inference Adapters, Streaming & Failover

## Overview

This spec covers **inference providers only**.

Aphelion talks to inference backends via direct HTTP. No SDKs. We own every byte on the wire: headers, payloads, retries, and streaming assembly.

This spec intentionally does **not** cover:

- the governor contract
- Codex-backed governor execution
- file storage
- vector stores / retrieval storage
- audio transcription / translation
- speech generation

Those are platform services, not inference adapters. OpenAI may satisfy both the inference interface here and the platform-service interfaces defined in `memory.md` and `media.md`.

The governor contract is defined separately in `governor.md`. Native inference providers are used by:

- the native governor backend
- optional provider-backed face rendering

## Scope

### v0 required

- one working inference adapter
- non-streaming completion
- tool call round-tripping
- provider-reported token usage
- retry classification for transient provider failures

### v0.5

- structured inference request/response objects
- optional streaming
- second inference provider
- runtime-level failover chain

### Deferred after v0.5

- provider-specific caching optimizations
- provider-specific thinking/reasoning controls
- OpenRouter quirks
- exact prompt-prefix reuse and cache-aware heuristics

## Inference Provider Interface

### v0 minimal contract

```go
type Provider interface {
    Complete(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error)
}
```

This is enough for a runnable first system.

### v0.5 target contract

```go
type InferenceProvider interface {
    Complete(ctx context.Context, req *InferenceRequest) (*InferenceResponse, error)
    Stream(ctx context.Context, req *InferenceRequest, cb StreamCallback) (*InferenceResponse, error)
    Name() string
    Model() string
    ContextWindow() int
}

type InferenceRequest struct {
    SystemPrompt   []ContentBlock
    Messages       []Message
    Tools          []ToolDef
    MaxTokens      int
    Temperature    *float64
    StopSequences  []string
    ThinkingMode   string
    ThinkingBudget int
}

type InferenceResponse struct {
    Content   string
    ToolCalls []ToolCall
    Usage     TokenUsage
}

type StreamCallback func(chunk StreamChunk)

type StreamChunk struct {
    Type     string // "text", "tool_call", "thinking", "usage"
    Text     string
    ToolCall *ToolCall
    Usage    *TokenUsage
}
```

The important split is:

- provider adapters translate request/response formats
- runtime owns assembly policy, failover, and most cache strategy

## Supported Inference Providers

### Anthropic

Anthropic is the first-class v0 inference backend.

Responsibilities:

- Messages API request/response translation
- tool-use and tool-result mapping
- usage extraction
- streaming support later via SSE

### OpenAI (inference only)

OpenAI should be supported as an inference backend, but only its **chat/reasoning inference** lives in this spec.

OpenAI file storage, vector stores, and transcription are deliberately handled elsewhere:

- retrieval / files: `memory.md`
- transcription / translation: `media.md`

### OpenRouter

OpenRouter is an inference gateway. It belongs here because it provides inference, model routing, and streaming in an OpenAI-compatible shape.

### Gemini

Gemini belongs here only as an inference backend.

### Ollama

Ollama belongs here only as a local inference backend.

## Staging Per Provider

### v0

- Anthropic only

### v0.5

- Anthropic
- OpenRouter
- OpenAI inference

### Deferred after v0.5

- Gemini
- Ollama

The reason for this staging is to avoid freezing the inference abstraction around a single narrow adapter while also avoiding four half-done adapters too early.

## Streaming

Streaming is deferred after the first working inference path.

When implemented:

- Anthropic and OpenAI use SSE
- Ollama uses newline-delimited JSON
- the provider adapter converts provider-native stream events into `StreamChunk`
- Telegram delivery/edit cadence remains a channel concern, not a provider concern

## Retries and Failover

### Retries

Transient retry handling should exist even in v0:

- retry on `429`, `500`, `502`, `503`
- exponential backoff
- bounded retry count

### Failover

Failover is **not** a provider concern. It is a runtime/orchestration concern layered above providers and governor backend selection.

The provider adapter should:

- return typed/provider-classified errors when possible
- avoid embedding failover policy internally

The runtime failover chain should:

- retry within one provider first
- fail over only on retryable exhaustion
- avoid failover on deterministic client/request errors

The native-provider failover chain should be explicit and ordered.

Example:

- governor backend: `codex`
- native fallback chain: `anthropic -> openrouter`

That means:

1. retry Codex within its own backend logic where appropriate
2. on bounded retry exhaustion or retryable runtime failure, fall back to the native provider chain
3. retry Anthropic first
4. on bounded retry exhaustion, fail over to OpenRouter
5. on deterministic client/request errors, stop rather than cascading through every provider

OpenRouter should be treated as an inference gateway fallback, not as the canonical primary provider contract for the system.

## Caching

Prompt caching is important, but most of its policy belongs to prompt assembly, sessions, and runtime orchestration rather than to the raw adapter itself.

So the split is:

- provider adapter: supports provider-native cache fields if the runtime asks for them
- runtime/system-prompt/session layers: decide when and where cache boundaries belong

### v0

- no explicit provider cache-control support required

### Deferred after v0

- Anthropic explicit cache controls
- OpenAI cached-token accounting
- OpenRouter TTL quirks
- cache-aware pruning and lookback safety

## Token Counting

We need approximate token counts for:

- context-window management
- compaction decisions
- cost diagnostics

Strategy:

- rough estimation for planning/thresholds
- provider-reported usage for accounting

## Tests

### v0

- **TestAnthropicComplete**: valid Anthropic response maps into internal response
- **TestAnthropicToolCall**: tool-use content maps into internal tool calls
- **TestAnthropicToolResult**: tool-result content maps back correctly
- **TestProviderRetryTransient**: transient provider failures retry with backoff
- **TestProviderNoRetryOnClientError**: deterministic client errors return immediately

### v0.5

- **TestProviderRequestMapping**: structured inference request maps correctly into provider payloads
- **TestAnthropicStream**: SSE stream maps into ordered `StreamChunk` values
- **TestOpenRouterComplete**: OpenRouter chat-completions response maps correctly
- **TestOpenAIComplete**: OpenAI inference response maps correctly
- **TestFailoverToSecondary**: primary inference provider exhausts retries, secondary succeeds
- **TestFailoverNoClientError**: deterministic request error does not trigger failover
- **TestCodexFallsBackToNativeChain**: Codex runtime failure falls back to native provider chain when configured

### Deferred

- **TestOpenRouterInference**
- **TestGeminiInference**
- **TestOllamaInference**
- **TestAnthropicCacheFields**
- **TestOpenAICachedUsage**
