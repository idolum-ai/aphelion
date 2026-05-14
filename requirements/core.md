# Core — Event Loop & Message Lifecycle

## Overview

Aphelion's core is a Go daemon that routes messages between Telegram and an LLM agent. Single binary, single process, Linux only. The runtime leans on Go's goroutine scheduler and Linux kernel primitives instead of frameworks.

This spec is **staged**. The initial "core runnable" milestone is the minimal end-to-end daemon: transport in, session load/save, governor decision, face rendering, transport out. Linux-native hardening features (pidfd, memfd, sandbox assembly, sub-agent sockets) remain part of the target architecture, but they are not required for the first usable runtime.

## Design Principles

1. **Goroutines are the concurrency model.** No event loop library. Each session turn runs in its own goroutine. The Go scheduler multiplexes on epoll.
2. **Linux-native.** Use kernel APIs directly: pidfd for process management, cgroups for sandboxing, memfd for secrets, unix sockets for sub-agents.
3. **No god objects.** Each concern is its own package. `main.go` stays thin: flags, config, dependency wiring, signal handling. Runtime orchestration lives in its own package. The core wires things together and gets out of the way.
4. **Message-oriented.** The core routes typed messages between components. It doesn't know what Telegram is or what Claude is.
5. **Single static binary.** `go build` → `scp aphelion phosphor:~/` → done.

## Architecture

```
┌───────────┐     ┌──────────┐     ┌──────────────┐     ┌──────────────┐
│ Telegram  │────▶│  Router  │────▶│   Runtime    │────▶│    turn      │
│           │◀────│          │◀────│ (house shell)│◀────│   Machine    │
└───────────┘     └──────────┘     └──────┬───────┘     └──────┬───────┘
                                          │                    │
                                     ┌────┴────┐        ┌──────┴────────┐
                                     │ Session │        │   pipeline    │
                                     │  Store  │        │ brokerage +   │
                                     └─────────┘        │ floor/render  │
                                                        └───────────────┘
```

### Components

- **Telegram**: Long-polls the Bot API. Normalizes updates into `InboundMessage`. Sends `OutboundMessage` back. Knows Telegram formatting. Doesn't know about LLMs.
- **Router**: Maps inbound messages to sessions by chat ID. Dispatches agent turns as goroutines. Enforces one-turn-at-a-time per session via per-session mutexes. Queues follow-up messages while busy, then compacts the queued slice into one follow-up turn.
- **Runtime**: House shell. Owns transport wiring, principal/scope resolution, session locking, background loops, and assembly of concrete ports for turn execution. Interactive DM and durable-group turns should share one explicit interactive-like assembly spine with bounded species specialization hooks.
- **turn**: Owns one-turn stage order and policy for interactive, durable-child, and maintenance species.
- **pipeline**: Owns governor/face conversational transformations (brokerage parsing, floor shaping, render/fallback contracts) consumed by turn/runtime.
- **Governor**: Owns the floor of a turn. This layer is named `Idolum (System)`. It may be backed by Codex or by the native provider/tool loop. `Aphelion` is the repo/service/harness that hosts it.
- **Face**: Authors the user-visible scene from the governor-owned floor.
- **Agent**: The native governor path. Runs a single conversational turn via the provider/tool loop.
- **Session Store**: SQLite via CGo. Persists conversation history, system prompt snapshots, metadata. Start with a single-connection SQLite access model (`SetMaxOpenConns(1)`); add a dedicated writer goroutine only if contention or correctness requires it.

## Status Aggregation Source Of Truth

Operational `/status` views combine multiple sources:

- router in-memory state (`active_turn_ids`, `queue_depth_by_chat`)
- `pending_decisions` durable rows
- session continuation state rows
- TES latest-turn, stale-running, and recovery-issued projections
- runtime stale-watchdog health flag/threshold

Pending totals must be deterministic from these rules:

1. queue depth > 0 contributes one `queue` pending item per chat
2. each durable pending decision contributes one `decision` item
3. continuation status `pending` or `approved` contributes one `continuation` item
4. each TES recovery issuance without a later terminal recovery event contributes one `recovery` item
5. each stale running turn contributes one `stale_turn` item

Status rendering should remain summary-first and bounded, but keep stable key labels for machine parsing.

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
5. Runtime loads session state from SQLite and prepares turn inputs
6. Runtime delegates to `turn.Machine`
7. Turn machine runs governor stage with pipeline contracts
8. Governor turn:
   a. If backend is native: assemble API messages (governor prompt + history + new message)
   b. HTTP call to inference provider
   c. If response has tool calls → execute tools → append results → goto 8b
   d. If response is text → floor complete
9. Turn machine runs face render/fallback stage via pipeline/runtime ports
10. Turn machine commits through persistence/delivery ports
11. Runtime adapter sends delivered scene or fallback text → OutboundMessage via Telegram
12. Router releases per-session mutex
```

### Concurrency

- **One turn at a time per session.** Per-session `sync.Mutex`. If messages arrive during a turn, they are queued and then compacted into one follow-up input after the active turn finishes.
- **Multiple sessions run concurrently.** Different ChatIDs don't block each other. Each turn is its own goroutine.
- **Tool execution is sequential within a turn.** Tools run in the agent's goroutine. Sub-agents are separate (see below).
- **Context cancellation.** Every turn gets a cancelable `context.Context` (no hard per-turn deadline by default). User controls (`/stop`, `/detach`, thinking-card controls) and SIGTERM cancel active contexts for graceful drain.

### Ownership Boundaries

- **`main.go` is boot only.** Parse flags, load config, construct dependencies, install signal handling, start transport/runtime.
- **`core.Router` is transport/provider agnostic.** It should not know about SQLite schema, Telegram formatting, or provider request shapes.
- **Runtime owns shell wiring.** Session lock/load orchestration entrypoints, transport adapters, and long-lived loops belong in `runtime/`, not in `main.go` or `core/`.
- **turn owns stage order.** Turn policy, stage sequencing, and commit contracts belong in `turn/`.
- **pipeline owns conversational transforms.** Brokerage/floor/render/fallback contract mechanics belong in `pipeline/`.
- **Adapters live at the edges.** Transport normalization, provider wire formats, Codex/native governor adapters, face adapters, and persistence-to-agent transcript conversion live in dedicated packages.

## Linux-Native Primitives

These are target architecture features and should be introduced incrementally after the runnable core is stable. They remain part of the design because they fit the project's Linux-only philosophy, but they are not a prerequisite for the first usable daemon.

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

### Tool sandboxing: full stack

Tool exec processes run inside a multi-layer sandbox. The primitives are Linux-native:

- **cgroups v2**: Memory, CPU, PID, IO limits. Transient cgroup per exec, cleaned up on exit.
- **Network namespaces**: deny-by-default isolated networking, plus helper-owned veth and nftables allowlist enforcement when a profile explicitly uses `network = "allowlist"`.
- **seccomp-bpf**: Restrict available syscalls. `moderate` blocks ptrace/mount/bpf/etc.
- **Capabilities**: Drop CAP_SYS_ADMIN, CAP_NET_ADMIN, etc.
- **User namespaces**: Map exec to nobody:nogroup.
- **Filesystem**: `hidden_paths` makes config/SSH/GPG invisible. Optional read-only root.

Full sandbox configuration: see `config.md` `[sandbox]` section.
Full sandbox assembly logic: see `security.md`.

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
// 4. Flush session store / pending writes
// 5. Close SQLite
// 6. Exit
```

## Error Handling

- **LLM provider errors**: Retry 429/500/503 with exponential backoff (max 3 retries). Surface persistent errors to user via Telegram.
- **Tool exec errors**: Capture combined stdout+stderr, return to LLM as error-flagged tool result.
- **Telegram send errors**: Retry once, then log and drop.
- **Panics**: `recover()` at the runtime turn boundary. Log stack trace, send generic error to user, session survives.

## Module Structure

```
aphelion/
├── main.go              # entrypoint, wiring, signal handling
├── config/
│   └── config.go        # TOML config loading, memfd credential storage
├── core/
│   ├── router.go        # message routing, session dispatch, per-session mutex
│   └── types.go         # InboundMessage, OutboundMessage, Media, TurnResult
├── runtime/
│   ├── runtime.go       # process shell wiring and background loops
│   ├── turn*.go         # runtime-to-turn adapters and species coordinators
│   └── *_runtime_test.go # runtime-domain integration suites by concern
├── turn/
│   ├── engine.go        # one-turn stage ordering machine
│   ├── stages.go        # governor/face/persist/deliver stage implementations
│   ├── policy.go        # turn-species policy defaults
│   └── ports.go         # governor/face/persistence/delivery interfaces
├── pipeline/
│   ├── brokerage.go     # brokerage parsing and ratification contracts
│   ├── material.go      # governor-output -> floor-material extraction
│   └── contracts.go     # execution/render contracts + policy helpers
├── governor/
│   ├── governor.go      # governor interface
│   ├── codex.go         # Codex-backed governor adapter
│   └── native.go        # native provider/tool-loop governor
├── face/
│   ├── face.go          # face interface
│   └── provider.go      # provider-backed face renderer
├── agent/
│   ├── turn.go          # RunTurn(): the native governor turn loop
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
│   ├── adapter.go       # session.Message <-> agent.Message conversion
│   └── compact.go       # context window compaction
├── workspace/
│   └── prompt.go        # workspace bootstrap/dynamic file loading
├── memory/
│   └── vectors.go       # embedding search (optional)
├── voice/
│   └── elevenlabs.go    # TTS
└── internal/
    ├── linux.go         # pidfd, memfd, cgroup helpers
    └── sse.go           # SSE stream parser
```

## What We're NOT Doing

- **No plugin system.** Add it to the codebase or don't.
- **No multi-node.** Single binary, single machine.
- **No channel sprawl.** Telegram is the primary control link and CLI is the
  maintenance surface. Future channels such as WhatsApp must be compiled-in code
  changes behind a small transport boundary, not plugins or an omnichannel
  product surface.
- **No cross-platform.** Linux only. `//go:build linux` on the whole project.
- **No web dashboard.** Telegram is the UI. Logs go to stderr/journald.
- **No provider SDKs in the native path.** Direct HTTP. We own every byte on the wire. Codex-backed governor support is a separate governor concern, not a reason to distort the native provider interface.
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
- **TestQueueCompaction**: Multiple messages arrive for the same ChatID while a turn is running → queued messages are compacted into one follow-up input processed after the current turn.
- **TestQueueCompactionKeepsLatestArtifactsOnly**: When queued messages include artifacts, compaction drops older queued artifacts and keeps only the newest queued artifact set.
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
