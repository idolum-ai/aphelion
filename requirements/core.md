# Core — Event Loop & Message Lifecycle

## Overview

Aphelion's core is an async event loop that routes messages between **channels** (Telegram, WhatsApp) and an **agent** (LLM + tools). It is deliberately simple: a single process, single agent, no clustering.

## Design Principles

1. **Async-first.** Everything is `asyncio`. Channels, providers, tools — all async.
2. **No god objects.** Hermes puts everything in a 9,400-line `AIAgent` class. We don't. Each concern gets its own module.
3. **Message-oriented.** The core doesn't know about LLMs or Telegram. It routes typed messages between pluggable components.
4. **Fail loud, recover quiet.** Errors are logged with full context. Recovery is automatic where safe (retry transient failures), explicit where not (surface to user).

## Architecture

```
┌─────────────┐     ┌──────────┐     ┌──────────────┐
│  Channel    │────▶│  Router  │────▶│    Agent     │
│  (Telegram) │◀────│          │◀────│  (LLM+Tools) │
└─────────────┘     └──────────┘     └──────────────┘
                         │
                    ┌────┴────┐
                    │ Session │
                    │  Store  │
                    └─────────┘
```

### Components

- **Channel**: Receives inbound messages, sends outbound messages. Knows message format (Telegram markdown, WhatsApp, etc). Does NOT know about LLMs.
- **Router**: Maps inbound messages to sessions, dispatches to the agent, routes responses back. Handles concurrency (one agent turn at a time per session, queue overflow).
- **Agent**: Runs a single conversational turn. Takes a session (messages + system prompt), produces a response (text + tool calls). Stateless between turns — all state lives in the session.
- **Session Store**: Persists conversation history, session metadata, system prompt snapshot. SQLite.

## Message Types

```python
@dataclass
class InboundMessage:
    """A message arriving from a channel."""
    channel: str            # "telegram", "whatsapp"
    chat_id: str            # channel-specific chat identifier
    sender_id: str          # channel-specific sender identifier
    sender_name: str        # display name
    text: str               # message text (may be empty for media-only)
    media: list[Media]      # attached images, audio, files
    reply_to: str | None    # message id being replied to
    metadata: dict          # channel-specific extras (message_id, timestamp, etc.)

@dataclass
class OutboundMessage:
    """A message going out to a channel."""
    chat_id: str
    text: str
    media: list[Media]
    reply_to: str | None    # reply to a specific inbound message
    parse_mode: str | None  # "markdown", "html", None
    reactions: list[str]    # emoji reactions to add to the inbound message

@dataclass
class Media:
    """An attachment."""
    type: str               # "image", "audio", "video", "document"
    data: bytes | None      # raw bytes (for small inline media)
    path: str | None        # local file path (for large media)
    url: str | None         # remote URL
    mime_type: str
    filename: str | None
```

## Turn Lifecycle

A "turn" is one complete cycle: user message in → agent processing → response out.

```
1. Channel receives raw message
2. Channel normalizes → InboundMessage
3. Router resolves session (by chat_id + channel)
4. Router acquires session lock (one turn at a time)
5. Router loads session state (history, system prompt)
6. Router calls Agent.run_turn(session, inbound)
7. Agent loop:
   a. Assemble API messages (system prompt + history + new message)
   b. Call LLM provider
   c. If response has tool calls → execute tools → append results → goto 7b
   d. If response is text → done
8. Agent returns AgentResponse (text, media, tool calls made, token usage)
9. Router persists updated session
10. Router converts AgentResponse → OutboundMessage(s)
11. Channel sends outbound message(s)
12. Router releases session lock
```

### Concurrency

- **One turn at a time per session.** If a message arrives while a turn is in progress, it's queued. Queue depth = 1 (latest message wins, older queued messages are dropped with a "I'm still thinking" ack).
- **Multiple sessions can run concurrently.** Different chat_ids don't block each other.
- **Tool execution is sequential within a turn.** No parallel tool calls (simplicity over speed for v1).

### Error Handling

- **LLM provider errors**: Retry transient (429, 500, 503) with exponential backoff. Surface persistent errors to user ("I'm having trouble reaching my brain right now").
- **Tool execution errors**: Capture stderr/stdout, feed back to LLM as tool result with error flag. Let the LLM decide what to do.
- **Channel send errors**: Retry once, then log and drop. Don't crash the event loop.
- **Unhandled exceptions**: Log full traceback, send generic error to user, continue event loop.

## Iteration Budget

Each turn has a configurable maximum number of LLM calls (default: 50). This prevents runaway tool-calling loops.

- At 70% budget: inject a warning into the next tool result ("You're running low on iterations, start wrapping up.")
- At 90% budget: inject urgent warning.
- At 100%: force stop, return whatever the last assistant message was.

## Shutdown

- SIGTERM / SIGINT → graceful shutdown.
- Finish any in-progress turn (up to 30s timeout).
- Persist all session state.
- Close channel connections.
- Exit.

## Module Structure

```
aphelion/
├── core/
│   ├── __init__.py
│   ├── loop.py          # async event loop, signal handlers
│   ├── router.py        # message routing, session resolution, concurrency
│   ├── types.py         # InboundMessage, OutboundMessage, Media, AgentResponse
│   └── errors.py        # error types, retry logic
├── agent/
│   ├── __init__.py
│   ├── turn.py          # run_turn(): the agent turn logic
│   └── budget.py        # iteration budget tracking
├── channels/
│   ├── __init__.py
│   ├── base.py          # Channel protocol/ABC
│   ├── telegram.py
│   └── whatsapp.py
├── providers/
│   ├── __init__.py
│   ├── base.py          # Provider protocol/ABC
│   ├── anthropic.py
│   ├── gemini.py
│   ├── openai.py
│   └── ollama.py
├── tools/
│   ├── __init__.py
│   ├── registry.py
│   ├── exec.py
│   ├── files.py
│   └── web.py
├── sessions/
│   ├── __init__.py
│   ├── store.py         # SQLite session store
│   └── compaction.py    # context window management
├── memory/
│   ├── __init__.py
│   ├── workspace.py     # file-based memory
│   └── vectors.py       # embedding search
├── automation/
│   ├── __init__.py
│   ├── heartbeat.py
│   └── cron.py
├── voice/
│   └── elevenlabs.py
└── config.py            # configuration loading
```

## What We're NOT Doing (vs OpenClaw / Hermes)

- **No plugin system.** If you need something, add it to the codebase.
- **No multi-node.** Single process, single machine.
- **No multi-agent orchestration.** One agent. Sub-agents are spawned as child processes.
- **No WebSocket gateway.** Channels connect directly. No intermediary server for UIs.
- **No web dashboard.** CLI + messaging channels are the interface.
- **No OpenAI Responses API compat layer.** We talk native Anthropic, Gemini, OpenAI Chat Completions, and Ollama.

## Language & Dependencies

- **Python 3.12+**
- **asyncio** for concurrency
- **httpx** for HTTP (async, connection pooling)
- **SQLite** (via aiosqlite) for session persistence
- **pydantic** for config validation (maybe — could also just be dataclasses)
- Minimal dependencies. No frameworks. No ORMs.

## Open Questions

- [ ] Do we want a CLI interface at all, or purely messaging-channel driven?
- [ ] WebSocket/SSE for local dev/debug, or just tail logs?
- [ ] Should the router be a simple async function or a proper class with lifecycle?
- [ ] Pydantic vs dataclasses for types?
