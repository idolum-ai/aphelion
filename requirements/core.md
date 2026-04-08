# Core — Event Loop & Message Lifecycle

## Overview

Aphelion's core is a Go daemon that routes messages between Telegram and an LLM agent. Single binary, single process, Linux only. The runtime leans on Go's goroutine scheduler and Linux kernel primitives instead of frameworks.

## Design Principles

1. **Goroutines are the concurrency model.** No event loop library. Each session turn runs in its own goroutine. The Go scheduler multiplexes on epoll.
2. **Linux-native.** Use kernel APIs directly: pidfd for process management, cgroups for sandboxing, memfd for secrets, unix sockets for sub-agents.
3. **No god objects.** Each concern is its own package. The core wires them together at startup and gets out of the way.
4. **Message-oriented.** The core routes typed messages between components. It doesn't know what Telegram is or what Claude is.
5. **Single static binary.** `go build` → `scp aphelion phosphor:~/` → done.

## Architecture

```
┌───────────┐     ┌──────────┐     ┌──────────────┐
│ Telegram  │────▶│  Router  │────▶│    Agent     │
│           │◀────│          │◀────│  (LLM+Tools) │
└───────────┘     └──────────┘     └──────────────┘
                       │
                  ┌────┴────┐
                  │ Session │
                  │  Store  │
                  └─────────┘
```

### Components

- **Telegram**: Long-polls the Bot API. Normalizes updates into `InboundMessage`. Sends `OutboundMessage` back. Knows Telegram formatting. Doesn't know about LLMs.
- **Router**: Maps inbound messages to sessions by chat ID. Dispatches agent turns as goroutines. Enforces one-turn-at-a-time per session via per-session mutexes. Queues overflow.
- **Agent**: Runs a single conversational turn. Stateless — takes session state in, returns a response. The turn loop (call LLM → execute tools → repeat) is a plain `for` loop in a goroutine.
- **Session Store**: SQLite via CGo. Persists conversation history, system prompt snapshots, metadata. Accessed through a single writer goroutine (SQLite's concurrency model).

## Message Types

```go
type InboundMessage struct {
    ChatID     int64
    SenderID   int64
    SenderName string
    Text       string
    Media      []Media
    ReplyTo    *int64          // message ID being replied to
    MessageID  int64
    Timestamp  time.Time
    Raw        json.RawMessage // full Telegram update, for anything we didn't extract
}

type OutboundMessage struct {
    ChatID    int64
    Text      string
    Media     []Media
    ReplyTo   *int64
    ParseMode string // "MarkdownV2", "HTML", ""
    Reactions []string
}

type Media struct {
    Type     string // "photo", "audio", "video", "document", "voice"
    Data     []byte // small inline media
    Path     string // local file path
    URL      string // remote URL
    MimeType string
    Filename string
}
```

## Turn Lifecycle

```
1. Telegram goroutine receives update from long-poll
2. Normalizes → InboundMessage
3. Router resolves session (by ChatID)
4. Router acquires per-session mutex
5. Router loads session state from SQLite
6. Router spawns goroutine: agent.RunTurn(ctx, session, inbound)
7. Agent turn loop:
   a. Assemble API messages (system prompt + history + new message)
   b. HTTP call to LLM provider (streaming via httpx-style chunked read)
   c. If response has tool calls → execute tools → append results → goto 7b
   d. If response is text → done
8. Agent returns TurnResult (text, media, tool log, token usage)
9. Router persists updated session to SQLite
10. Router sends TurnResult → OutboundMessage via Telegram
11. Router releases per-session mutex
```

### Concurrency

- **One turn at a time per session.** Per-session `sync.Mutex`. If a message arrives during a turn, it's buffered in a channel (cap 1, latest wins).
- **Multiple sessions run concurrently.** Different ChatIDs don't block each other. Each turn is its own goroutine.
- **Tool execution is sequential within a turn.** Tools run in the agent's goroutine. Sub-agents are separate (see below).
- **Context cancellation.** Every turn gets a `context.Context` with a timeout. SIGTERM cancels all active contexts → graceful drain.

## Linux-Native Primitives

### Process management: pidfd

Tool exec and sub-agents spawn child processes. We manage them via `pidfd_open(2)`:

```go
// pidfd gives us a file descriptor for a child process.
// Race-free: no PID reuse bugs. Pollable via epoll (Go runtime handles this).
fd, err := unix.PidfdOpen(pid, 0)
// Wait via pidfd — integrates with Go's netpoller
unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0)
```

### Secrets: memfd_create

API keys and tokens live in anonymous memory, never on disk:

```go
// Create anonymous memory-backed fd. MFD_CLOEXEC = invisible after exec.
fd, err := unix.MemfdCreate("credentials", unix.MFD_CLOEXEC)
// Write credentials, seek back to 0, read when needed.
// /proc/self/fd/<N> exists but the file has no name on disk.
```

Credentials are loaded from environment variables or a single encrypted config at startup, written to memfd, and the original sources are zeroed.

### Tool sandboxing: cgroups v2

Each tool exec runs in a transient cgroup with resource limits:

```go
// Create cgroup for this exec invocation
cgroupPath := fmt.Sprintf("/sys/fs/cgroup/aphelion/exec-%s", execID)
os.MkdirAll(cgroupPath, 0755)
os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte("512M"), 0644)
os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte("100000 100000"), 0644) // 1 CPU
os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte("64"), 0644)

// Move child process into cgroup
os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0644)
```

Cgroup is cleaned up when the exec completes. A runaway tool can't OOM the host.

### Sub-agent communication: unix domain sockets

Sub-agents are child processes that communicate over `AF_UNIX`:

```go
// Parent creates socketpair
fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
// Child inherits one fd, parent keeps the other.
// SO_PEERCRED gives us the child's PID/UID for free.
```

Zero network overhead. No port allocation. No localhost exposure.

## HTTP Client

We don't use provider SDKs. All LLM providers are REST APIs over HTTPS. One shared HTTP client:

```go
// Shared transport with connection pooling and keep-alive.
// Provider adapters build requests, parse responses.
transport := &http.Transport{
    MaxIdleConns:        10,
    MaxIdleConnsPerHost: 5,
    IdleConnTimeout:     90 * time.Second,
    // TLS config as needed
}
client := &http.Client{Transport: transport}
```

Streaming responses are read via `resp.Body` as `io.Reader` — chunked transfer encoding is handled by the HTTP stack. We parse SSE lines ourselves (trivial).

## Iteration Budget

Each turn has a max LLM call count (default: 50).

```go
type Budget struct {
    Max      int
    Used     int
    Caution  float64 // 0.7 — inject "wrapping up" nudge
    Warning  float64 // 0.9 — inject "stop now" nudge
}

func (b *Budget) Tick() (warning string, exhausted bool) {
    b.Used++
    ratio := float64(b.Used) / float64(b.Max)
    switch {
    case ratio >= 1.0:
        return "", true
    case ratio >= b.Warning:
        return "⚠️ Last iteration. Return your final response now.", false
    case ratio >= b.Caution:
        return "You're running low on iterations. Start wrapping up.", false
    default:
        return "", false
    }
}
```

Budget warnings are injected into the next tool result content, not as separate messages (preserves cache prefix).

## Shutdown

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()

// On signal:
// 1. Stop accepting new Telegram updates
// 2. Cancel all active turn contexts (30s grace period)
// 3. Wait for in-flight turns to drain
// 4. Flush session store
// 5. Close SQLite
// 6. Exit
```

## Error Handling

- **LLM provider errors**: Retry 429/500/503 with exponential backoff (max 3 retries). Surface persistent errors to user via Telegram.
- **Tool exec errors**: Capture combined stdout+stderr, return to LLM as error-flagged tool result.
- **Telegram send errors**: Retry once, then log and drop.
- **Panics**: `recover()` in the turn goroutine. Log stack trace, send generic error to user, session survives.

## Module Structure

```
aphelion/
├── main.go              # entrypoint, wiring, signal handling
├── config/
│   └── config.go        # TOML config loading, memfd credential storage
├── core/
│   ├── router.go        # message routing, session dispatch, per-session mutex
│   └── types.go         # InboundMessage, OutboundMessage, Media, TurnResult
├── agent/
│   ├── turn.go          # RunTurn(): the agent turn loop
│   └── budget.go        # iteration budget
├── telegram/
│   ├── bot.go           # Bot API client, long-polling, send
│   └── format.go        # MarkdownV2 conversion, message splitting
├── provider/
│   ├── provider.go      # Provider interface
│   ├── anthropic.go     # Anthropic Messages API + caching
│   ├── gemini.go        # Gemini API
│   ├── openai.go        # OpenAI Chat Completions
│   └── ollama.go        # Ollama local
├── tool/
│   ├── registry.go      # tool registration and dispatch
│   ├── exec.go          # shell exec with cgroup sandboxing
│   ├── files.go         # read, write, edit
│   └── web.go           # HTTP fetch
├── session/
│   ├── store.go         # SQLite session store
│   └── compact.go       # context window compaction
├── memory/
│   ├── workspace.go     # file-based workspace (SOUL.md, etc.)
│   └── vectors.go       # embedding search (optional)
├── automation/
│   ├── heartbeat.go     # periodic agent turns
│   └── cron.go          # scheduled jobs
├── voice/
│   └── elevenlabs.go    # TTS
└── internal/
    ├── linux.go         # pidfd, memfd, cgroup helpers
    └── sse.go           # SSE stream parser
```

## What We're NOT Doing

- **No plugin system.** Add it to the codebase or don't.
- **No multi-node.** Single binary, single machine.
- **No multi-channel.** Telegram only.
- **No cross-platform.** Linux only. `//go:build linux` on the whole project.
- **No web dashboard.** Telegram is the UI. Logs go to stderr/journald.
- **No provider SDKs.** Direct HTTP. We own every byte on the wire.
- **No ORM.** Raw SQL via `database/sql` + `mattn/go-sqlite3`.

## Dependencies (minimal)

- `mattn/go-sqlite3` — SQLite via CGo
- `golang.org/x/sys/unix` — Linux syscalls (pidfd, memfd, cgroup)
- Standard library for everything else (net/http, encoding/json, os/exec, crypto/tls)

## Tests

Each test should be a standalone Go test in the corresponding package.

### core/router

- **TestRouteToSession**: Inbound message with ChatID X → router creates/retrieves session X, dispatches to agent.
- **TestSessionMutex**: Two concurrent inbound messages for the same ChatID → second blocks until first turn completes. Verify sequential execution (no interleaving).
- **TestConcurrentSessions**: Two concurrent inbound messages for different ChatIDs → both turns run in parallel. Verify wall-clock time < 2x single turn.
- **TestQueueOverflow**: Three messages arrive for the same ChatID while a turn is running → only the latest queued message is processed after the current turn. Earlier queued messages are dropped.
- **TestSessionResolution**: Messages from different ChatIDs map to different sessions. Same ChatID always maps to same session.

### agent/turn

- **TestSimpleTurn**: Mock provider returns text response → TurnResult contains that text, no tool calls logged.
- **TestToolCallLoop**: Mock provider returns tool call → tool executes → result fed back → provider returns text → done. Verify the loop ran exactly 2 LLM calls.
- **TestMultipleToolCalls**: Mock provider returns tool calls for 3 iterations before final text. Verify iteration count = 4.
- **TestProviderError**: Mock provider returns 500 → retry with backoff → succeeds on retry 2. Verify retry count and backoff delay.
- **TestProviderPersistentError**: Mock provider returns 500 on all retries → TurnResult contains user-facing error message.
- **TestToolError**: Tool returns error → error is included in tool result message → provider gets it and responds with text.
- **TestContextCancellation**: Cancel context mid-turn → turn exits cleanly, no goroutine leak.

### agent/budget

- **TestBudgetCaution**: At 70% of max → returns caution warning string.
- **TestBudgetWarning**: At 90% → returns urgent warning.
- **TestBudgetExhausted**: At 100% → returns exhausted=true.
- **TestBudgetUnderLimit**: Below 70% → no warning, not exhausted.

### core/types

- **TestInboundMessageDefaults**: Construct InboundMessage with minimal fields → zero values are correct.
- **TestMediaTypes**: Construct Media with each type → type string matches.

### Shutdown

- **TestGracefulShutdown**: Send SIGTERM → verify in-flight turn completes, session is persisted, Telegram poller stops, SQLite closes.
- **TestShutdownTimeout**: In-flight turn takes >30s → verify it's force-cancelled after grace period.

### Integration (requires SQLite)

- **TestFullTurnCycle**: Inbound message → router → agent (mock provider) → outbound message → session persisted in SQLite. End-to-end with real SQLite.
- **TestSessionPersistence**: Run a turn, kill the process, restart, send another message → history from first turn is loaded from SQLite.

## Decisions

- **Config format: TOML.** Human-readable, supports comments, Go ecosystem default. `BurntSushi/toml` (zero transitive deps).
- **Session store: SQLite.** Relational queries (by chat ID, by timestamp), CLI-inspectable (`sqlite3 sessions.db`), single-writer model matches our per-session mutex. `mattn/go-sqlite3` via CGo.
- **Logging: `log/slog` (stdlib).** Structured, zero-dependency, JSON handler for production (journald-friendly), text handler for dev. Filter by session, chat ID, provider without regex.
