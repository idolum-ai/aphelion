# `telegram/` boundary

`telegram/` is Aphelion's Telegram transport and Bot API adapter. It owns
Telegram wire types, Bot API request/response behavior, update polling, media
upload/download helpers, and normalization between Telegram updates and core
transport records.

The shortest boundary sentence is:

> `telegram/` owns Telegram transport mechanics, not command policy or runtime
> orchestration.

A Telegram update becoming available is not enough to decide what the system is
allowed to do. Command routing, approval handling, session persistence,
capability policy, provider execution, and final delivery sequencing belong in
higher packages.

## Owned responsibilities

`telegram` owns behavior when it is about Telegram as a transport surface:

- Bot API client construction and HTTP transport behavior;
- sending, editing, deleting, and formatting Telegram messages;
- media and artifact upload/download requests;
- long-polling updates and advancing Telegram offsets through explicit
  poller/checkpoint hooks;
- Telegram wire types, message/callback/reaction records, sender names, reply
  context, durable group metadata, and Telegram-specific normalization;
- Markdown/formatting helpers whose only job is to make Telegram payloads valid;
- converting Telegram-specific updates into `core.InboundMessage`-style
  transport records for the rest of the runtime.

The package may depend on low-level configuration, principal, and core transport
types. It should remain usable as a transport adapter without constructing the
runtime shell or session store.

## Non-owned responsibilities

`telegram` must not become a command router, runtime coordinator, session store,
or policy authority. Code belongs elsewhere when it owns:

- slash-command behavior, admin command routing, or callback action policy;
- approval decisions, capability grants, tool authority, or child-agent policy;
- governor/face sequencing, provider calls, or turn-stage orchestration;
- durable session schema, migrations, transcripts, review events, or mission
  ledger persistence;
- deploy/restart behavior, background service lifecycle, or process supervision;
- business rules about what a Telegram message means beyond transport
  normalization.

Those concerns should live in packages such as `internal/telegramcommands`,
`internal/telegramdecision`, `runtime`, `turn`, `session`, or `tool` depending on
which boundary owns the decision.

## Current subsystem map

| Cluster | Representative files | Telegram-owned role | Boundary pressure |
| --- | --- | --- | --- |
| Client and transport | `client.go`, `client_transport.go`, `client_send.go`, `client_edit.go`, `client_file.go`, `client_media.go`, `client_updates.go` | Bot API requests, response decoding, upload/download/edit/send/delete helpers | Must not decide command or approval policy |
| Polling and checkpoints | `poller.go`, `poller_checkpoint.go` | Receive Telegram updates and call explicit checkpoint/handler hooks | Must not own session schema or runtime scheduling |
| Wire and normalization types | `types.go`, `reply_context.go`, `message_sender_name.go`, `artifacts.go` | Telegram records and conversion into core transport facts | Must not become durable storage records |
| Formatting | `format.go` | Telegram-safe Markdown/text formatting | Must not absorb presentation policy beyond Telegram payload validity |
| Durable group metadata | `durable_groups.go` | Telegram-specific group identity/config parsing | Must not own durable-agent authority or child policy |
| Tests | `*_test.go` | Transport contract and formatting/poller/client invariants | Should exercise Bot API/transport behavior, not full runtime flows |

## Import direction

Good dependency direction:

```text
runtime/internal command packages  --->  telegram/  --->  core/config/principal
```

Forbidden dependency direction:

```text
telegram/  -X->  runtime/
telegram/  -X->  turn/
telegram/  -X->  pipeline/
telegram/  -X->  session/
telegram/  -X->  tool/
telegram/  -X->  internal/telegramcommands/
telegram/  -X->  internal/telegramdecision/
```

This direction keeps Telegram a transport adapter. Higher packages may interpret
Telegram callbacks, messages, and delivery results, but `telegram` itself should
only expose typed transport facts and Bot API behavior.

## Growth rules

A new file belongs in `telegram` when most of these are true:

1. It models Telegram API payloads, responses, updates, callbacks, reactions,
   files, media, chats, users, or message formatting.
2. It sends or receives Telegram Bot API requests, or helps make those payloads
   valid.
3. It normalizes Telegram wire details into core transport records without
   deciding command semantics.
4. It can be tested without a runtime, session store, provider, tool registry,
   or capability grant state machine.
5. It preserves the distinction between transport observation and authority to
   act.

Prefer another package when code is mostly command policy, approval/review
handling, durable session writes, runtime turn orchestration, tool/capability
authority, or service lifecycle.

## Cleanup posture

`telegram` is not currently a deep refactor target. The package is already
mostly cohesive: one package, focused transport imports, and tests around client,
formatting, polling, durable group parsing, and message normalization.

The useful guardrail is a small boundary contract: keep Telegram wire behavior
here, and keep decisions about what Telegram messages authorize or trigger in the
command, decision, runtime, session, or tool packages that own those decisions.
