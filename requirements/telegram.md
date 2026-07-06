# Telegram — Bot API Integration

_Current status:_ Telegram is the primary radio link and the only current
operator channel. Future channels must be compiled-in transport boundaries, not
plugins or an omnichannel product surface.

## Overview

Telegram is the primary channel for Aphelion. The telegram package handles long-polling for updates, normalizing them into `InboundMessage`, sending responses as `OutboundMessage`, and all Telegram-specific formatting.

We talk to the Telegram Bot API directly via HTTP. No SDK, no library.

## Bot API Basics

**Base URL**: `https://api.telegram.org/bot<token>/`

All methods are HTTP POST with JSON body. Responses are JSON with `ok: bool`, `result: T`, and optional `description: string`.

**Auth**: Bot token in the URL path. Token comes from sealed memfd.

## Polling

We use long-polling via `getUpdates`. No webhooks — simplifies deployment (no TLS cert, no public IP needed).

```go
type Poller struct {
    token    string
    client   *http.Client
    offset   int64          // Track last processed update_id
    timeout  int            // Long-poll timeout (seconds), from config
    handler  func(Update)   // Callback for each update
    logger   *slog.Logger
}

func (p *Poller) Run(ctx context.Context) error {
    for {
        if err := ctx.Err(); err != nil {
            return err
        }
        updates, err := p.getUpdates(ctx)
        if err != nil {
            // Log and retry after short backoff
            p.logger.Warn("getUpdates failed", "error", err)
            select {
            case <-ctx.Done():
                return ctx.Err()
            case <-time.After(5 * time.Second):
                continue
            }
        }
        for _, update := range updates {
            p.offset = update.UpdateID + 1
            p.handler(update)
        }
    }
}
```

**Parameters**:
- `offset`: last update_id + 1
- `limit`: 100 (default)
- `timeout`: from config (`telegram.poll_timeout`, default 30s)
- `allowed_updates`: `["message", "edited_message", "callback_query"]`

`callback_query` matters because Telegram is also the first decision surface for:

- busy-turn interruption choices
- stop-word confirmation while busy
- risky tool approvals

Those should all flow through one runtime decision broker rather than separate ad hoc handlers.

## Side Threads

Telegram private chats support lightweight side threads for parallel operator
work without adding another transport surface.

Requirements:

- The main chat session is thread `0`; ordinary messages continue to route there.
- `/thread` creates the next per-chat numeric side thread, records the thread
  row and guide message, and returns a compact usage guide with an absorb
  button.
- `/thread <message>` creates the next per-chat numeric side thread and routes
  `<message>` as the first user turn in that thread.
- A message beginning with `(thread N)` routes to existing open thread `N`; the
  prefix is stripped before storage.
- `/threads` lists open and recent side threads and exposes absorb buttons for
  open threads.
- `/threads` exposes an `Analyze` button when open threads exist. The callback
  is recorded as recoverable callback-work ingress, then queues ordinary
  main-chat work with bounded open-thread evidence; it does not analyze inline
  in the callback. The queued prompt uses operator-facing display thread
  numbers, includes age/last-active/session-state evidence when available, and
  asks for a structured triage note rather than a flat status list.
- `/absorb N` closes thread `N` and records a compact outcome note in the main
  chat session.
- Thread-visible replies, progress cards, and stream edits are prefixed with
  `(thread N)` as presentation text.
- Replies to side-thread guide cards, progress cards, thread-created messages,
  accepted ingress, and outbound replies route back to that thread from durable
  ledger evidence.
- Thread identity is typed state on `core.InboundMessage` and session scope,
  not hidden transcript prose.
- Side threads have independent session, plan, progress, and recovery state.
  They share the same Telegram transport and same configured principal rules.
- Absorb is bookkeeping and pruning. It does not automatically approve curated
  memory writes or copy the full thread transcript into thread `0`.
- Analyze is bookkeeping. It does not close, promote, absorb, or mutate side threads.
- Open side threads are eligible for default-on stale-thread reminders.
- Reminder selection is deterministic and bounded: hard eligibility, scoring,
  max-per-sweep budget, per-thread cooldown, and one-reminder-per-activity-epoch
  dedupe.
- Reminder state must suppress noisy repeats: pending, ignored, resumed,
  absorbed, and expired states prevent duplicate reminders for the same activity
  epoch.
- Reminder copy must avoid exposing sensitive-looking thread openings by
  default; privacy-softened summaries are preferred over raw topic text.
- Replying to a reminder must resume the original side-thread scope through
  durable Telegram message/thread ledger evidence.
- `ignore` must suppress/edit the reminder without deleting thread content.
- `absorb` must delegate to the existing absorb semantics rather than inventing
  a separate close path.
- `/threads remind` is an admin diagnostic command. The background sweep is the
  product path.
- `/doctor` and health/status projections should expose enough reminder evidence
  for repair: pending reminder counts and sweep telemetry, without making those
  projections an authority source.

## Update Normalization

We only handle `message` updates for v1. Each incoming message is normalized into `core.InboundMessage`.

```go
func normalizeUpdate(update Update) *core.InboundMessage {
    msg := update.Message
    if msg == nil {
        return nil // Skip non-message updates
    }
    
    inbound := &core.InboundMessage{
        ChatID:     msg.Chat.ID,
        SenderID:   msg.From.ID,
        SenderName: buildDisplayName(msg.From),
        Text:       msg.Text,
        MessageID:  msg.MessageID,
        Timestamp:  time.Unix(int64(msg.Date), 0),
        Raw:        rawJSON(update),
    }
    
    // Caption for media messages
    if inbound.Text == "" && msg.Caption != "" {
        inbound.Text = msg.Caption
    }
    
    // Reply context
    if msg.ReplyToMessage != nil {
        id := int64(msg.ReplyToMessage.MessageID)
        inbound.ReplyTo = &id
    }
    
    // Artifact extraction
    inbound.Artifacts = extractArtifacts(msg)
    
    return inbound
}
```

### Display name building

```go
func buildDisplayName(user *User) string {
    if user.Username != "" {
        return user.Username
    }
    name := user.FirstName
    if user.LastName != "" {
        name += " " + user.LastName
    }
    return name
}
```

### Artifact extraction

Telegram attachments should normalize into channel-neutral `core.Artifact` values rather than a growing list of Telegram-only media branches.

At minimum, the transport should normalize:

- `photo`
- `document`
- `voice`
- `audio`
- `video`
- `video_note`
- `animation`
- `sticker`
- `contact`
- `location`
- `venue`
- `poll`

Normalization and capability rules:

- major inbound attachment classes normalize into artifacts
- deep handling is governed by the artifact capability envelope, not just by Telegram type
- images, PDFs, text-like documents, and audio may download bytes for deterministic handling
- videos, animated stickers, and structured Telegram objects may remain metadata-first

Telegram file downloads still use the Bot API two-step flow:

1. `getFile` to resolve `file_path`
2. `https://api.telegram.org/file/bot<token>/<file_path>` to download bytes

Downloads should happen only when the artifact capability envelope justifies local bytes for this turn.

For outbound DM turns, Telegram is the visible surface of the face layer:

1. user message enters raw
2. governor decides
3. face authors the visible scene
4. Telegram sends the delivered scene

Outbound delivery must treat Telegram's message size limit as a first-class constraint rather than a rare failure case. Long replies should be split into sequential Telegram messages before delivery instead of attempting one oversized `sendMessage`.

## Outbound Media Replies

Ordinary assistant replies may include native Telegram media delivery.

The current path is:

1. governor produces ordinary reply text
2. runtime strips material outbound directives from that text
3. face authors the visible caption or reply text
4. Telegram chooses the native send method per media item

Current directive contract:

- `MEDIA: <path>`
- `[[audio_as_voice]]`

These directives are runtime material, not user-visible prose.

That means:

- the directives are removed before visible delivery
- the visible caption is still authored through the normal face path when text remains
- media-only replies may send an empty caption rather than a fake placeholder

### Supported Telegram outbound methods

The Telegram client should choose native upload methods per media kind:

- `sendPhoto`
- `sendDocument`
- `sendVideo`
- `sendAudio`
- `sendVoice`
- `sendAnimation`

If a reply contains multiple media items, Telegram delivery may send them sequentially rather than as one album. The first media item may carry the caption; overflow text should be delivered as follow-up text messages.

### Safe outbound media roots

For ordinary replies, local file paths must stay inside the active scope roots.

Current allowed local roots are:

- scope working root
- scope shared-memory root
- scope user-memory root

Remote URLs are not part of this first implementation tranche.

### Voice precedence

Explicit outbound media takes precedence over synthesized voice mode.

That means:

- if a reply includes explicit media, Telegram sends that media
- runtime should not synthesize a separate voice reply for the same turn
- `[[audio_as_voice]]` marks an outbound audio file for native Telegram `sendVoice`

## Durable Telegram Groups

Telegram groups are not ordinary house-principal sessions.

An admitted durable Telegram group should run as a durable child:

- child-local session scope
- child-local charter and live policy
- group members remain child-local subjects, not house principals
- bounded upward synthesis through the existing review path

### Admission

Group ingress is inert by default.

Only groups explicitly configured in `[[telegram.durable_groups]]` should activate the durable-group adapter.

## Durable Telegram DM relay

Durable children may expose a bounded direct-message transport lane in Telegram.

Relay entrypoint syntax:

- `agent:<agent_id> <message>`

Routing rules:

- relay-shaped private messages can bypass ordinary principal pre-filtering in the poller
- runtime authorization still enforces child-scoped access (`allowed_telegram_user_ids` or admin)
- the durable child executes inside its own durable scope and replies in the same chat when policy allows
- unresolved private messages that do not match the relay syntax remain dropped by the principal gate

### Child execution

When an admitted group message arrives:

1. Telegram normalizes the update into `InboundMessage`
2. runtime resolves the durable child by `agent_id`
3. the durable child executes inside its own isolated scope
4. the child uses only its own configured LLM bootstrap
5. the child may emit:
   - a bounded local reply
   - a bounded upward review artifact
   - both, depending on live policy

The child must not inherit the parent's governor credentials or provider API keys.

### Parent-owned delivery

Even when the group turn executes inside the child runtime, Telegram delivery remains parent-owned.

That means:

- the child computes the turn result and any local reply text
- the parent process sends the actual Telegram message
- outbound bookkeeping remains in the parent session/store path

This keeps external-channel transport ownership separate from child-local reasoning.

### Child-local LLM bootstrap

The current durable-group adapter supports two child bootstrap shapes:

- `native`
  - `anthropic`
  - `openai`
  - `openrouter`
  - `gemini`
  - `ollama`
- `codex`

If the configured bootstrap is missing or invalid, the group child should fail rather than silently falling back to the parent's LLM path.

Current implementation detail:

- native children may still use the ordinary face render path inside the child turn
- codex children currently serialize the floor through the fallback path inside the child turn rather than running a child-local face-provider render
- Telegram delivery remains parent-owned in both cases

## Bot Commands

Telegram command discovery should be explicit. On startup, Aphelion should register its command list with `setMyCommands` so Telegram clients can show the available slash commands.

At minimum for current implementation:

- `/start`
- `/help`
- `/status`
- `/health`
- `/tailnet`
- `/agents`
- `/context`
- `/memory`
- `/mission`
- `/model`
- `/stop`
- `/new`
- `/detach`
- `/restart`
- `/reinstall`

These commands should be handled directly by the Telegram/runtime boundary rather than routed through the ordinary governor turn path.

Telegram Bot API command identifiers should use underscore form rather than hyphen form so they remain valid commands and display correctly in Telegram clients.

### `/tailnet`

`/tailnet` is an admin-only Tailnet operator projection. It must show compact
status, private parent status URL when known, registry surface evidence, grant
binding evidence, and drift/issue evidence without mutating live Tailscale
policy.

Controls must be button-driven for finite choices:

- `Refresh`, `Surfaces`, `Grants`, and optional `Open Status`
- paged `Surface <n>` and `Grant <n>` detail buttons
- `Revoke` only from a resolved surface detail card

Surface and grant callbacks must use compact tokens that re-resolve against the
current registry on click. Missing or colliding tokens are stale and must not
mutate state. Surface revoke requires a second confirmation and writes only the
local registry revoke/audit event.

### Button order language

Binary decision prompts should keep a stable side language:

- left button = stop/deny/reject
- right button = continue/approve/allow

This includes continuation approval and durable decision prompts, so users do not need to relearn button-side meaning between flows.

Inline button labels must stay compact for Telegram surfaces: non-empty and at
most two words. Put scope, phase, and safety detail in the surrounding message,
not the button label.

### Repository commit approval diagnostics

`git commit` is a repository-history mutation and must be explained as its own
nested approval gate. A continuation approval can resume the plan turn, but it
does not by itself approve the lower-level repository-history proposal.

When a repository commit approval is denied or expires, the user-facing denial
and the tool error should include:

- `gate: repository_commit`
- required approval kind (`proposal_approval`)
- required approval status (`denied` or `expired`)
- default choice (`deny`)
- timeout/default-deny reason when known
- whether continuation approval covered it (`false` / `no`)
- why not: continuation approval resumes the plan turn; git commit opens a
  separate repository-history proposal
- next action: approve the specific git commit proposal card, or request a fresh
  commit approval if it expired

### `/start`

Show a short intro and the command list.

### `/help`

Show the current command list and what each command does.

### `/status`

`/status` is button-driven (no command arguments).

The first response is a summary-first status snapshot plus inline controls. Chat-scoped `/status` must act as a deterministic operator triage card, not just a telemetry projection: it should show `Status`, `Why`, `Now`, state-specific `Next`, attention/backlog items, `Evidence` with as-of time/source, runtime, and a bounded details pointer. Admin status drilldowns use the same operator-panel shape and keep raw telemetry in `/health trace`.

User controls:

- `This Chat`
- `Pending Only`
- `Refresh`

When `/status` is scoped to a side thread, the first button is `This Thread`.
The status message is recorded in the thread callback ledger, and `This Thread`,
`Pending Only`, and `Refresh` callbacks must keep rendering that side thread's
session state. Thread-local cards must not expose global admin drilldowns.

Admin-only controls (visible only to Telegram admins):

- `System Overview`
- `Hot Chats`
- `Find Chat`
- `Durables`

`Find Chat` must remain callback-first:

- show recent active/pending chats as drill-down buttons (`status:chat:<chat_id>`)
- avoid slash-command parameters for view selection

Raw diagnostic status payloads must include stable key labels so they remain machine-parseable later. Telegram `/status` panels should consume typed status snapshots directly and render bounded human operator cards.

At minimum, status snapshots should surface:

- active turns count + per-chat ids
- queue depth per chat
- live in-flight turn phase (`face_proposal|brokerage|governor|render|persist|deliver`) when available
- pending decision prompts (kind/chat/age/stale)
- continuation state (pending/approved/revoked + remaining turns)
- latest persisted turn-run state (status/kind/last activity/last tool/error)
- persisted operation + plan sidecar status (`operation`, `plan_step`, `plan_progress`)
- hidden-input carryover state from floor metadata (categories + provenance summary)
- operation completion evidence with typed reason codes, so readable status summaries can explain missing evidence without matching exact prose
- delivery-path status (in-flight, delivered, persisted-not-delivered, delivery-failed)
- detached/outstanding work counters (decisions, continuations, recovery, stale runs)
- stale running turn indicators
- stale-turn watchdog/restart health indicator
- deterministic `Next:` guidance for the chat state (for example: inspect pending decisions, wait/refresh active work, resolve blockers, run `/health diagnose` for stale/recovery, or send the next request when idle)
- `Evidence:` line with snapshot as-of time and status projection source

### `/health trace`

`/health trace` should stay command-routed (not a callback-only status subview) and bypass ordinary governor turns the same way `/status` does.

`/health trace` is a diagnostic surface for when `/status` is too compressed:

- include chat status plus `Trace Chat:` details:
  - latest turn request text
  - last tool preview/result/error
  - decoded exec command when preview includes `{"command": ...}`
- prepend `Quick Read:` summary when readable summaries are available
- chunk output to stay within Telegram message limits

Admin callers should additionally receive:

- full system status block
- `Trace System:` details (pending-kind counters + latest turn rollups by chat)
- full durables status block

### Natural language durable external-channel bootstrap

Admin DM inputs that clearly request creating an external-channel durable child should be normalized into a safe wizard-driving instruction before ordinary turn routing.

Normalization goals:

- preserve the user's intent in plain text
- force the workflow onto `durable_agent` wizard actions (`wizard_start`, `wizard_answer`, `wizard_show`, `wizard_finalize`, `connection_test`, `activate`)
- explicitly prohibit `exec`/`go run` routes for this workflow
- require one-question-at-a-time collection for missing wizard fields
- carry forward a detected external channel address when present in user text

### Durable wizard inline controls

When outbound text includes an durable-child wizard machine block (`action: durable-agent wizard show`), Telegram delivery should attach inline controls to that message.

Requirements:

- step-aware preset answer buttons for structured wizard steps (autonomy, wakeup mode, summarize PDFs, cadence/interval, charter/capability/retention presets)
- in-progress control row: left `Cancel`, right `Refresh`
- ready control row: left `Cancel`, right `Finalize`
- callbacks are admin-only
- callbacks must be stale-safe:
  - mismatched current-step callbacks are acknowledged as stale and ignored
  - malformed wizard cards are acknowledged as stale and ignored
- valid callbacks must execute deterministic wizard actions (`wizard_answer`, `wizard_show`, `wizard_finalize`, `wizard_cancel`) and edit the same Telegram message in place

### `/context`

Show a read-only current-context panel for the current chat or side-thread lane.

At minimum it should surface:

- current lane/scope
- operation and plan summaries when present
- recent session context preview
- evidence/source labels
- `Writes: none`

Buttons:

- `Ask Me` queues durable callback-work clarification about context assumptions; it must not write memory or mutate state.
- `Refresh` reloads the same panel in place.

### `/memory`

Show a read-only memory-state panel. It is not a mutation surface.

At minimum it should surface:

- durable store counts
- session/semantic recall counts
- selected source and query seed
- recall preview items as evidence candidates, not approved memories
- `Writes: none`

Buttons:

- `Session`, `Shared`, and `Local` switch read-only source views.
- `Ask Me` queues durable callback-work clarification about memory assumptions or recall items; it must not write memory or mutate state.
- `Refresh` reloads the selected source in place.

### `/stop`

Cancel the in-flight turn for the current DM session and drop any queued follow-up messages that have not started yet.

This command must be real, not decorative. If Telegram advertises `/stop`, the user should not have to wait for the current turn to finish before the stop takes effect.

### `/detach`

Detach the caller from pending state in the current DM chat.

At minimum this should:

- stop active turn execution for the chat
- clear queued follow-up messages for the chat
- revoke continuation approval for the chat
- detach durable pending decisions for owner key `chat:<chat_id>:sender:<sender_id>`

The command should be idempotent and safe to repeat.

### `/restart`

Admin-only forced gateway restart.

Before process exit, restart should detach pending approvals when `telegram.detach_pending_on_restart = true`.

Default behavior should set `detach_pending_on_restart` to enabled.

### `/model`

Admin-only model slot surface.

The command should show current model routing and provide inline controls for
slot-scoped model, thinking, and speed choices when the configured provider
surface allows them.

The surface should stay small and role-shaped:

- `Persona`: face proposal/render work
- `Main`: interactive and recovery governor work
- `Health`: diagnostic work
- `Children`: default durable-child bootstrap work
- `Status`: readable status summaries
- `Heartbeat`: heartbeat outreach and reflection work
- `Curiosity`: silent curiosity observations

Slot overrides persist until changed or cleared. Clearing a slot returns it to
the configured default. Runtime overrides should not mutate recipe files,
constitutional files, or the base config on disk.

Status, heartbeat, and curiosity expose a `Cheap` preset that restores the
install's cheap-lane default. OpenAI slots may expose `speed=fast`, which maps
to the provider's priority service tier. Standard speed omits a service tier.
Non-OpenAI slots should not show speed controls or accept fast mode.

## Outbound Delivery

Telegram delivery should be chunk-aware.

### Message size

Telegram text messages have a practical size ceiling of about 4096 characters.

Aphelion should therefore:

- split oversized outbound text into multiple messages before delivery
- prefer paragraph boundaries before hard length splitting
- reply the first chunk to the selected reply anchor when one is available
- reply each following chunk to the message id of the preceding delivered chunk
- preserve formatting on each chunk when possible
- fall back to plain text for a chunk if formatted delivery fails

The first delivered chunk should remain the canonical outbound message id for ordinary bookkeeping. For thread-aware replies, every delivered chunk should also be recorded as a thread message anchor so later user replies can recover the thread by navigating through Telegram reply links.

### Thread reply anchors

For non-agent Telegram side threads, visual `(thread N)` prefixes are not enough. Aphelion should maintain a durable last-message anchor per `(chat_id, thread_id)` and use it as the default reply target for assistant responses in that thread.

- inbound non-agent thread messages update the thread anchor to the user's Telegram message id
- outbound non-agent thread replies use the latest known anchor when present, falling back to the inbound message id when absent
- each delivered outbound chunk updates the thread anchor to its own Telegram message id
- replies to any recorded chunk should resolve back to the same thread through normal reply lookup
- durable-agent lanes keep their own reply behavior and must not inherit ordinary thread anchors

This keeps long model responses navigable even when Telegram displays them as multiple messages or the operator is working across several side threads at once.

### Streaming and edits

Streaming edits should also respect Telegram size limits.

If a live edited message grows past the safe edit size, the delivery layer may finalize that message and continue in a follow-up message rather than failing the turn.

### Error visibility

The Telegram client must not discard API error bodies on non-200 responses.

Returned/logged errors should preserve:

- HTTP status
- Telegram `description` when present
- a bounded response-body excerpt when no structured description exists

This matters because delivery failures such as:

- `message is too long`
- parse-mode/entity failures
- `message is not modified`

must be distinguishable operationally.

## DM Admission

Telegram is the primary ingress path, so admission starts here.

For private chats:

- if the sender resolves to a configured principal, route the message into the DM session
- if the sender does not resolve to a configured principal, do not create a session and optionally send a fixed denial response

Admission is config-owned. The Telegram layer should treat it as explicit
principal policy, not as "all private chats are valid."

```go
type DMDecision struct {
    Route      bool
    SendNotice bool
    NoticeText string
}

func shouldHandleDM(msg *Message, principal *Principal) DMDecision {
    if principal == nil {
        return DMDecision{
            Route:      false,
            SendNotice: true,
            NoticeText: "This bot is not enabled for your account.",
        }
    }
    return DMDecision{Route: true}
}
```

## Group Behavior

### Mention detection

In groups, the bot only responds when:
1. The message mentions the bot via `@botusername` (check `entities` for `mention` type)
2. The message is a reply to one of the bot's messages
3. The message contains a bot command (`/command@botusername`)

```go
func shouldRespond(msg *Message, botUsername string) bool {
    // Private chats are further filtered by admission policy.
    if msg.Chat.Type == "private" {
        return true
    }
    
    // Check for @mention
    for _, entity := range msg.Entities {
        if entity.Type == "mention" {
            mentioned := msg.Text[entity.Offset:entity.Offset+entity.Length]
            if strings.EqualFold(mentioned, "@"+botUsername) {
                return true
            }
        }
    }
    
    // Check if replying to bot's message
    if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
        if msg.ReplyToMessage.From.IsBot && msg.ReplyToMessage.From.Username == botUsername {
            return true
        }
    }
    
    return false
}
```

### Sender prefix for group sessions

In shared group sessions, each user message is prefixed with the sender name:

```go
func prefixForGroup(msg *core.InboundMessage, chatType string, groupScope string) string {
    if chatType == "private" || groupScope == "per_user" {
        return msg.Text // No prefix needed
    }
    return fmt.Sprintf("[%s]: %s", msg.SenderName, msg.Text)
}
```

## Sending Messages

### Text messages

```go
func (s *Sender) SendText(ctx context.Context, msg core.OutboundMessage) (int64, error) {
    // 1. Convert LLM markdown to Telegram MarkdownV2
    formatted := formatMarkdownV2(msg.Text)
    
    // 2. Split if over 4096 chars
    chunks := splitMessage(formatted, 4096)
    
    var lastMsgID int64
    for i, chunk := range chunks {
        body := map[string]interface{}{
            "chat_id":    msg.ChatID,
            "text":       chunk,
            "parse_mode": "MarkdownV2",
        }
        
        // Reply to original message on first chunk only
        if i == 0 && msg.ReplyTo != nil {
            body["reply_parameters"] = map[string]interface{}{
                "message_id": *msg.ReplyTo,
            }
        }
        
        resp, err := s.call(ctx, "sendMessage", body)
        if err != nil {
            // Fallback: strip MarkdownV2 and send as plain text
            body["text"] = stripMarkdown(chunk)
            delete(body, "parse_mode")
            resp, err = s.call(ctx, "sendMessage", body)
            if err != nil {
                return 0, err
            }
        }
        lastMsgID = resp.Result.MessageID
    }
    
    return lastMsgID, nil
}
```

### Typing indicator

Send `sendChatAction` with `action: "typing"` while the agent is processing:

```go
func (s *Sender) SendTyping(ctx context.Context, chatID int64) error {
    return s.callVoid(ctx, "sendChatAction", map[string]interface{}{
        "chat_id": chatID,
        "action":  "typing",
    })
}
```

Start a goroutine that sends typing every 5 seconds until the turn completes.

### Reactions

```go
func (s *Sender) SetReaction(ctx context.Context, chatID int64, messageID int64, emoji string) error {
    return s.callVoid(ctx, "setMessageReaction", map[string]interface{}{
        "chat_id":    chatID,
        "message_id": messageID,
        "reaction":   []map[string]interface{}{{"type": "emoji", "emoji": emoji}},
    })
}
```

### Media sending

```go
func (s *Sender) SendPhoto(ctx context.Context, chatID int64, photo core.Media, caption string) error {
    // Use multipart/form-data for file upload
    // Or pass URL/file_id directly
}

func (s *Sender) SendDocument(ctx context.Context, chatID int64, doc core.Media, caption string) error { /* ... */ }
func (s *Sender) SendAudio(ctx context.Context, chatID int64, audio core.Media, caption string) error { /* ... */ }
func (s *Sender) SendVoice(ctx context.Context, chatID int64, voice core.Media, caption string) error { /* ... */ }
```

### Review digests to the admin DM

When `review_events` are delivered, they are sent as normal Telegram text messages to the admin DM. No separate dashboard is required.

The text should be clearly labeled, for example:

```text
[Review digest]
User: alice
Turns: 12-18
Summary:
...
```

These messages should also be recorded into the admin session history so the admin can respond naturally in the same DM.

## Live Feedback — Streaming & Tool Progress

When the agent is working, the user should see what's happening — not just a typing indicator.

### Streaming text (edit-in-place)

As the LLM streams tokens, we progressively edit a single Telegram message:

1. **First chunk arrives** → `sendMessage` with initial text + cursor `▉`
2. **Every ~300ms** → `editMessageText` with accumulated text + cursor
3. **Stream complete** → final `editMessageText` without cursor

This gives a "typing in real time" feel. The cursor makes it obvious the message is still generating.

If a streamed reply exposes an internal persona-context request marker, the
runtime must reconcile the already-sent stream by editing it to the sanitized or
fallback visible reply when possible. Successful and failed reconciliations
should be recorded as typed execution events so the visible transcript and
ledger do not diverge silently.

```go
type StreamEditor struct {
    sender    *Sender
    chatID    int64
    replyTo   *int64
    messageID int64     // ID of the message being edited
    buffer    string    // Accumulated text
    lastEdit  time.Time
    interval  time.Duration // 300ms default
    cursor    string        // " \u2589" (block cursor)
    done      bool
}

func (e *StreamEditor) OnChunk(text string) {
    e.buffer += text
    if time.Since(e.lastEdit) >= e.interval {
        e.flush()
    }
}

func (e *StreamEditor) Finish() {
    e.done = true
    e.flush() // Final edit without cursor
}

func (e *StreamEditor) flush() {
    display := e.buffer
    if !e.done {
        display += e.cursor
    }
    formatted := formatMarkdownV2(display)
    if e.messageID == 0 {
        // First chunk: send new message
        e.messageID = e.sender.SendText(...)
    } else {
        // Edit existing message
        e.sender.EditText(e.chatID, e.messageID, formatted)
    }
    e.lastEdit = time.Now()
}
```

**Overflow handling**: If accumulated text exceeds 4096 chars, finalize the current message (edit without cursor) and start a new one for overflow.

**Fallback**: If `editMessageText` fails (some edge cases), fall back to sending a new message instead.

### Tool progress (accumulated edit)

While the agent is in the tool-call loop, a separate progress message shows what tools are actually running:

```
Working on it...
- Inspecting files
- Writing memory files
- Updating config
```

The message is edited in place. Raw tool starts are rewritten into semantic phases by default, and repeated adjacent phases may be aggregated.

This feedback must be driven by real tool lifecycle events, not by assistant narration. If no tool has actually started, the system should not claim that it is "studying the codebase", "inspecting files", or doing any other background work. Typing alone is not enough to justify those claims.

```go
type ToolProgressReporter struct {
    sender    *Sender
    chatID    int64
    messageID int64       // Progress message ID (0 = not sent yet)
    entries   []Entry     // Rolling semantic steps
    mode      string      // "all" | "new" | "off"
    style     string      // "semantic" | "raw"
    window    int         // Visible steps before omission line appears
}

func (r *ToolProgressReporter) OnToolStart(name string, input json.RawMessage) {
    if r.mode == "off" {
        return
    }
    entry := classifyForProgress(name, input, r.style)
    if r.mode == "new" && alreadySeen(entry.Key) {
        return
    }
    r.entries = appendOrMerge(r.entries, entry)

    text := renderProgress(r.entries, r.window)
    if r.messageID == 0 {
        r.messageID = r.sender.SendPlainText(r.chatID, text)
    } else {
        r.sender.EditPlainText(r.chatID, r.messageID, text)
    }
}
```

**Modes** (configurable):
- `"all"` — Show every tool phase transition (default)
- `"new"` — Only show new tool phases, deduplicating repeats
- `"off"` — No tool progress messages

### Progress readability

Telegram tool progress should optimize for human readability, not raw argument fidelity.

By default, the live progress message should show semantic phases such as:

- `Inspecting files`
- `Writing memory files`
- `Updating config`
- `Restarting service`

It should not dump raw `exec` payloads or long shell commands into the chat unless the operator explicitly enables a raw trace mode.

The intended hierarchy is:

1. deterministic local semantic rewriting
2. bounded aggregation of repeated steps
3. optional future model-assisted summarization

The semantic progress surface should remain truthful:

- it must still be driven by actual tool starts
- it may simplify phrasing
- it must not fabricate work that has not started
- it must not conceal failure or fallback state

### Progress window

Tool progress is a rolling view, not a full execution log.

- Telegram should show only the most recent semantic steps
- earlier steps may be summarized as omitted
- the visible window should be configurable

The full execution/audit trail remains in machine-authored session and turn-run records rather than the Telegram progress artifact.

**Cleanup**: After the turn completes, optionally delete the progress message (or leave it for context). Configurable.

### Truthfulness rule

Telegram feedback should preserve the distinction between:

- **model-only reasoning**: typing indicator only
- **real tool-backed activity**: progress message driven by actual tool starts
- **detached background work**: watcher-style updates tied to a persisted process/task record

The user should never be left to infer that background work is happening merely because the assistant said so.

### Config

```toml
[telegram]
# ... existing fields ...

# Streaming
stream_edit_interval = "300ms"    # How often to edit the streaming message
stream_cursor = " \u2589"         # Cursor shown during streaming

# Tool progress
tool_progress = "all"             # "all" | "new" | "off"
tool_progress_style = "semantic"  # "semantic" | "raw"
tool_progress_window = 4          # Visible semantic steps before older ones are omitted
tool_progress_cleanup = false     # Delete progress message after turn completes
```

### Restart-aware progress

If a restart or hard interruption happens while a progress message is active:

1. the system should retain a structured machine-authored record of the interrupted run
2. the governor should later analyze the interruption in the maintenance ledger
3. future progress/watcher systems may edit or supersede the stale progress message, but the source of truth remains the structured run record rather than the UI artifact

## MarkdownV2 Formatting

LLMs output standard markdown. Telegram expects MarkdownV2. The conversion is non-trivial.

### Characters that must be escaped

In MarkdownV2, these characters must be backslash-escaped outside of formatting constructs:
```
_ * [ ] ( ) ~ ` > # + - = | { } . !
```

### Conversion rules

```go
func formatMarkdownV2(input string) string {
    // 1. Parse the LLM's markdown into an AST (or use regex-based conversion)
    // 2. Convert:
    //    **bold** → *bold*  (MarkdownV2 uses single * for bold)
    //    *italic* → _italic_
    //    ```code blocks``` → ```code blocks``` (same, but content must be escaped differently)
    //    `inline code` → `inline code` (same)
    //    [text](url) → [text](url) (but escape special chars in text, and ( ) in URL)
    //    > blockquote → >blockquote (MarkdownV2 blockquotes)
    //    - list items → • list items (or \- escaped)
    // 3. Escape all remaining special characters in non-formatted text
    // 4. Return MarkdownV2 string
}
```

### Fallback strategy

MarkdownV2 formatting is fragile — a single unescaped character rejects the whole message. Our strategy:

1. **Try MarkdownV2 first.**
2. **If Telegram returns error 400 with "can't parse entities"** → strip all MarkdownV2 formatting and resend as plain text.
3. **Log the failed MarkdownV2** at debug level for diagnosis.

This is the same approach Hermes uses and it's battle-tested.
See [`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md)
for the attribution and departure record behind this reference.

## Message Splitting

Telegram's limit is 4096 characters per message. We split at natural boundaries:

```go
func splitMessage(text string, maxLen int) []string {
    if len(text) <= maxLen {
        return []string{text}
    }
    
    var chunks []string
    for len(text) > 0 {
        if len(text) <= maxLen {
            chunks = append(chunks, text)
            break
        }
        
        // Find split point: prefer paragraph break, then line break, then space
        splitAt := maxLen
        if idx := strings.LastIndex(text[:maxLen], "\n\n"); idx > maxLen/2 {
            splitAt = idx
        } else if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
            splitAt = idx
        } else if idx := strings.LastIndex(text[:maxLen], " "); idx > maxLen/2 {
            splitAt = idx
        }
        
        chunks = append(chunks, text[:splitAt])
        text = strings.TrimLeft(text[splitAt:], "\n ")
    }
    
    return chunks
}
```

**MarkdownV2 splitting caveat**: Splitting inside a formatting construct (e.g., mid-code-block) breaks the message. The splitter must be aware of open/close markers and prefer split points outside formatting.

## Attachment Processing Pipeline

When a user sends media, it needs to be converted into something the LLM can use. Each media type has a different pipeline.

For the current Aphelion runtime, deep interpretive handling remains intentionally narrower than Telegram's full attachment surface:

- `photo`
- image `document`
- PDF `document`
- `voice`

For current implementation of the artifact system, the transport should still normalize the broader major Telegram attachment classes into artifacts even when their handling remains metadata-first:

- `audio`
- `video`
- `video_note`
- `animation`
- `sticker`
- `contact`
- `location`
- `venue`
- `poll`

Deferred:

- deeper video understanding
- sticker semantics beyond bounded metadata handling
- generic binary content understanding beyond metadata-first handling

The runtime must not silently imply unsupported media was actually processed.

Longer-term broad Telegram file support should not be implemented as a growing list of Telegram-only branches. The transport should normalize Telegram attachments into the channel-neutral artifact model in `artifacts.md`, and any model-side deliberation over meaning/retention should follow `artifact-brokerage.md`.

### Photos → Vision input

Photos are passed directly to the LLM as image content blocks.

Aphelion should use a **vision-first** path:

1. resolve principal before download
2. bound file size before or during download
3. download the largest Telegram photo variant
4. include the image directly in the native inference request
5. include caption text alongside the image when present

If the default governor backend is Codex and the turn includes supported image media, the runtime may route that turn through the native provider chain for that turn.

```go
func processPhoto(ctx context.Context, client *Client, fileID string) (*ContentBlock, error) {
    // 1. Download via getFile + HTTP GET
    data, mimeType, err := client.DownloadFile(ctx, fileID)
    // 2. Base64 encode for API
    b64 := base64.StdEncoding.EncodeToString(data)
    // 3. Return as image content block
    return &ContentBlock{
        Type: "image",
        Source: &ImageSource{
            Type:      "base64",
            MediaType: mimeType, // "image/jpeg", "image/png"
            Data:      b64,
        },
    }, nil
}
```

The content block is inserted into the user message alongside the text. If the message has both text and a photo, the user message becomes a multi-content-block message:
```json
{"role": "user", "content": [
    {"type": "image", "source": {"type": "base64", ...}},
    {"type": "text", "text": "What's in this image?"}
]}
```

### Documents → Text extraction or vision

```go
func processDocument(ctx context.Context, client *Client, doc *Document) (*ContentBlock, error) {
    data, _, err := client.DownloadFile(ctx, doc.FileID)
    
    switch {
    case isTextFile(doc.MimeType, doc.FileName):
        // .txt, .md, .py, .go, .js, .json, .csv, .yaml, .toml, .sh, .log
        return &ContentBlock{Type: "text", Text: string(data)}, nil
        
    case doc.MimeType == "application/pdf":
        // Current Aphelion path: extract text locally first.
        // Raw provider-native PDF document blocks are deferred.
        return &ContentBlock{Type: "text", Text: extractPDFText(data)}, nil
        
    case isImageFile(doc.MimeType):
        // Uncompressed images sent as documents (PNG, JPG without Telegram compression)
        return processAsImage(data, doc.MimeType)
        
    default:
        // Binary files: just note the metadata
        return &ContentBlock{
            Type: "text",
            Text: fmt.Sprintf("[File attached: %s (%s, %d bytes)]", 
                doc.FileName, doc.MimeType, len(data)),
        }, nil
    }
}

func isTextFile(mime string, name string) bool {
    textMimes := []string{"text/", "application/json", "application/xml", 
        "application/yaml", "application/toml", "application/x-sh"}
    for _, t := range textMimes {
        if strings.HasPrefix(mime, t) { return true }
    }
    textExts := []string{".txt", ".md", ".py", ".go", ".js", ".ts", ".rs",
        ".json", ".yaml", ".yml", ".toml", ".csv", ".sh", ".log", ".html", ".css"}
    ext := strings.ToLower(filepath.Ext(name))
    for _, e := range textExts {
        if ext == e { return true }
    }
    return false
}
```

### Voice messages

Ordinary Telegram media messages are routed through the runtime's automatic
media handling without a blocking processing-choice or retention callback
prompt. Non-blocking keep buttons may still be offered for the operator to keep
media permanently/locally.

### Deferred media classes

The following remain intentionally deferred in Aphelion:

- richer semantic video analysis beyond the configured native media provider
- sticker semantics beyond bounded metadata
- arbitrary binary files

Unsupported inbound media should surface as a clear bounded note rather than being silently dropped when practical.

### Voice/audio/video → Agent decides

Voice, audio, and video should route without a processing-choice inline keyboard.
The runtime records the media handling as agent-decide and lets the normal
persona/governor path determine whether to transcribe, analyze, inspect metadata,
or merely keep a reference. Voice/audio may still show the separate retention
button: `Keep audio`.

### Stickers → Auto-process (unambiguous)

Stickers are simple enough to always auto-process:

```go
func processSticker(sticker *Sticker) *ContentBlock {
    text := fmt.Sprintf("[Sticker: %s", sticker.Emoji)
    if sticker.SetName != "" {
        text += fmt.Sprintf(" from set '%s'", sticker.SetName)
    }
    text += "]"
    return &ContentBlock{Type: "text", Text: text}
}
```

### The principle

| Media type | Intent clear? | Action |
|---|---|---|
| Photo | Yes (vision) | Auto-process |
| Text document | Deferred in current runtime | Metadata or unsupported note |
| PDF | Yes (read it) | Extract text, then auto-process |
| Image-as-document | Yes (vision) | Auto-process |
| Sticker | Deferred | Metadata or unsupported note |
| Voice | Yes | Existing transcription path |
| Video | Deferred | Unsupported note |
| Audio file | Deferred | Unsupported note |
| Binary file | Deferred | Metadata or unsupported note |

Consistent with our design principle: **auto-process only where the runtime actually has a coherent path**.
```

### Size limits

- Telegram Bot API: max 20MB download
- Anthropic image input: max 20MB per image, max 5 images per message
- We check size before download and skip with a note if too large

```go
const maxDownloadSize = 20 * 1024 * 1024 // 20MB

func (c *Client) DownloadFileChecked(ctx context.Context, fileID string, maxSize int) ([]byte, string, error) {
    info, err := c.GetFile(ctx, fileID)
    if info.FileSize > maxSize {
        return nil, "", fmt.Errorf("file too large: %d bytes (max %d)", info.FileSize, maxSize)
    }
    return c.downloadFromPath(ctx, info.FilePath)
}
```

### Config

```toml
[telegram.media]
download_max_size = "20MB"        # Max file download size
auto_vision_photos = true          # Automatically include photos as vision input
auto_vision_documents = true       # Image documents are treated as vision input
extract_pdf_text = true            # Small PDFs are extracted locally to text
max_pdf_bytes = "8MB"              # Bound local PDF extraction work
```

### Tests

- **TestProcessPhoto**: Photo file_id → downloaded, base64 encoded, returned as image content block.
- **TestProcessDocumentText**: .py file → content returned as text block.
- **TestProcessDocumentPDF**: PDF → returned as document content block.
- **TestProcessDocumentImage**: .png sent as document → processed as image.
- **TestProcessDocumentBinary**: .zip → metadata-only text block.
- **TestVoiceAgentDecideWithoutProcessingButtons**: Voice message → routed immediately with agent-decide metadata and no media-processing keyboard.
- **TestAudioKeepPermanentButton**: Audio message → separate durable-retention button remains available.
- **TestProcessSticker**: Sticker with emoji → auto-processed, description text block.
- **TestFileTooLarge**: 25MB file → error, not downloaded.
- **TestMultiContentMessage**: Photo + caption → multi-block user message (image + text).

## File Download

For media the agent needs to process (images for vision, audio for transcription):

```go
func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
    // 1. POST getFile with file_id
    // 2. Get file_path from response
    // 3. GET https://api.telegram.org/file/bot<token>/<file_path>
    // 4. Return bytes
    // Note: max 20MB for files via Bot API
}
```

## Telegram API Types (minimal)

We define only the types we need, not the full Bot API:

```go
type Update struct {
    UpdateID int64    `json:"update_id"`
    Message  *Message `json:"message"`
}

type Message struct {
    MessageID      int64           `json:"message_id"`
    From           *User           `json:"from"`
    Chat           *Chat           `json:"chat"`
    Date           int             `json:"date"`
    Text           string          `json:"text"`
    Caption        string          `json:"caption"`
    Entities       []MessageEntity `json:"entities"`
    ReplyToMessage *Message        `json:"reply_to_message"`
    Photo          []PhotoSize     `json:"photo"`
    Document       *Document       `json:"document"`
    Audio          *Audio          `json:"audio"`
    Voice          *Voice          `json:"voice"`
    Video          *Video          `json:"video"`
    VideoNote      *VideoNote      `json:"video_note"`
    Sticker        *Sticker        `json:"sticker"`
}

type User struct {
    ID        int64  `json:"id"`
    IsBot     bool   `json:"is_bot"`
    FirstName string `json:"first_name"`
    LastName  string `json:"last_name"`
    Username  string `json:"username"`
}

type Chat struct {
    ID       int64  `json:"id"`
    Type     string `json:"type"` // "private", "group", "supergroup", "channel"
    Title    string `json:"title"`
    Username string `json:"username"`
}

type MessageEntity struct {
    Type   string `json:"type"` // "mention", "bot_command", "url", "code", "pre", etc.
    Offset int    `json:"offset"`
    Length int    `json:"length"`
}

type PhotoSize struct {
    FileID   string `json:"file_id"`
    Width    int    `json:"width"`
    Height   int    `json:"height"`
    FileSize int    `json:"file_size"`
}

// Document, Audio, Voice, Video, VideoNote, Sticker — similar shape with FileID
```

## Interruption Handling — Message While Busy

When a user sends a message while the agent is mid-turn (tools running, LLM streaming), we don't silently queue it. We give the user control.

### Flow

```
1. User sends message while agent is busy
2. Aphelion immediately replies with an inline keyboard:
   
   "I'm still working on the previous request. What would you like to do?"
   
   [ Stop ]  [ Finish ]

3a. User taps "Stop":
    - Cancel the current turn's context (ctx.Cancel())
    - Agent turn exits cleanly (context cancellation is already handled)
    - Delete the inline keyboard message
    - Route the new message as a fresh turn
    - The new message includes context: "[Previous request was interrupted. Last tool output: ...]"

3b. User taps "Finish":
    - Queue the new message
    - Edit the keyboard message to: "Got it — I'll process your message next. ⏳"
    - After current turn completes, compact queued messages into one follow-up input and process that as the next turn
    - During compaction, keep only artifacts from the newest queued message; drop older queued artifacts

3c. No tap (timeout 30s):
    - Default to "Finish" (queue the message)
    - Edit keyboard message to: "Queued your message — processing after current task."
```

### Implementation

```go
type InterruptHandler struct {
    sender   *Sender
    router   *core.Router
}

func (h *InterruptHandler) OnMessageWhileBusy(ctx context.Context, msg core.InboundMessage, cancelFn context.CancelFunc) {
    // Send inline keyboard
    kbMsgID := h.sender.SendInlineKeyboard(ctx, msg.ChatID, 
        "I'm still working on the previous request. What would you like to do?",
        []InlineButton{
            {Text: "Stop", CallbackData: "interrupt:stop"},
            {Text: "Finish", CallbackData: "interrupt:queue"},
        },
        &msg.MessageID, // Reply to the user's new message
    )
    
    // Wait for callback or timeout
    select {
    case cb := <-h.awaitCallback(ctx, kbMsgID, 30*time.Second):
        switch cb {
        case "interrupt:stop":
            cancelFn() // Cancel current turn
            h.sender.DeleteMessage(ctx, msg.ChatID, kbMsgID)
            h.router.RouteImmediate(ctx, msg) // Process new message now
        case "interrupt:queue":
            h.router.Enqueue(msg)
            h.sender.EditText(msg.ChatID, kbMsgID, "Got it — I'll process your message next. ⏳")
        }
    case <-time.After(30 * time.Second):
        // Default: queue it
        h.router.Enqueue(msg)
        h.sender.EditText(msg.ChatID, kbMsgID, "Queued your message — processing after current task.")
    }
}
```

This decision path should be brokered by a shared pending-decision layer rather than duplicating queue/wait logic in the Telegram transport and the tool runtime.

The transport owns:

- rendering inline buttons
- acknowledging callback queries
- editing or deleting the prompt message

The broker owns:

- decision ids
- pending state
- timeout resolution
- delivering the resolved choice back to the waiting caller

### Callback query handling

Telegram sends `callback_query` updates when users tap inline buttons. We need to handle these:

```go
func (p *Poller) handleUpdate(update Update) {
    if update.CallbackQuery != nil {
        p.callbackHandler(update.CallbackQuery)
        return
    }
    if update.Message != nil {
        p.messageHandler(normalizeUpdate(update))
    }
}
```

Update `allowed_updates` to include `"callback_query"`.

The callback path should not be treated as a second message stream. It is a response to an existing pending decision.

The normal flow should be:

1. pending decision created by runtime
2. Telegram renders inline keyboard with callback data containing the decision id
3. callback handler resolves that decision id
4. waiting runtime path resumes with the chosen result

If a decision was detached (for example via `/detach` or restart-time detach), callback resolution should:

- acknowledge the callback query
- return a stale/no-longer-active message
- avoid any side effects

Status callbacks follow the same transport rules:

- callback ids are encoded as `status:<mode>` or `status:chat:<chat_id>`
- admin-only modes (`system`, `hot`, `find`, cross-chat drill-down) must be denied for non-admin callers
- callback queries should be acknowledged
- status messages should be edited in place when possible, preserving inline buttons
- if output exceeds Telegram limits, split deterministically and send overflow as follow-up text chunks

### Router integration

The router's `Route()` method changes slightly:

```go
func (r *Router) Route(ctx context.Context, msg InboundMessage) {
    lock, session := r.resolveSession(msg.ChatID)
    
    if !lock.TryLock() {
        // Session is busy — trigger interrupt handler instead of silent queue
        if r.interruptHandler != nil {
            r.interruptHandler.OnMessageWhileBusy(ctx, msg, r.activeCancels[msg.ChatID])
        } else {
            r.enqueue(msg.ChatID, msg) // Fallback: queue for later compaction
        }
        return
    }
    // ... rest unchanged
}
```

### Config

```toml
[telegram]
# Interruption handling
interrupt_buttons = true          # Show stop/continue buttons when busy
interrupt_timeout = "30s"         # Auto-queue after this timeout
```

### Tests

- **TestInterruptStop**: User sends message while busy → taps Stop → current turn cancelled, new message processed.
- **TestInterruptQueue**: User sends message while busy → taps Finish → message queued, then included in the next compacted follow-up turn.
- **TestInterruptTimeout**: No tap → message auto-queued after 30s.
- **TestInterruptKeyboardSent**: Message while busy → inline keyboard reply sent to user.
- **TestInterruptCallbackAck**: Callback query → answerCallbackQuery sent (Telegram requires this).

## Stop Words — Confirmation, Not Assumption

Some systems silently abort agent turns on trigger words like "wait", "stop", "cancel". This is fragile — "wait" often means "hold on, I'm adding more" not "abort everything."

Aphelion never silently acts on ambiguous intent. Instead, we show a confirmation button.

### Flow

```
1. User sends a message matching a stop pattern while agent is busy
   Patterns: "wait", "stop", "cancel", "nevermind", "nvm", "hold on", "abort"
   (case-insensitive, whole-message or message starts with)

2. Aphelion immediately replies with an inline keyboard:
   
   "🛑 Stop the current task?"
   
   [ Yes, stop ]  [ Keep going ]
   
   (And the user's message is preserved — it might contain follow-up context)

3a. User taps "Yes, stop":
    - Cancel current turn
    - Delete the keyboard message
    - If the user's message was ONLY a stop word ("wait", "stop"), discard it
    - If the user's message had additional content ("wait, actually do X instead"),
      route the full message as a new turn

3b. User taps "Keep going":
    - Queue the user's message for after the current turn
    - Edit keyboard to "Got it — I'll process your message next. ⏳"

3c. No tap (timeout 15s):
    - Default: keep going (non-destructive)
    - Queue the message
```

### Stop pattern detection

```go
var stopPatterns = []string{
    "wait", "stop", "cancel", "nevermind", "nvm", "hold on", "abort", "halt",
}

func isStopWord(text string) bool {
    lower := strings.ToLower(strings.TrimSpace(text))
    for _, p := range stopPatterns {
        if lower == p || strings.HasPrefix(lower, p+" ") || strings.HasPrefix(lower, p+",") {
            return true
        }
    }
    return false
}

func isOnlyStopWord(text string) bool {
    lower := strings.ToLower(strings.TrimSpace(text))
    for _, p := range stopPatterns {
        if lower == p {
            return true
        }
    }
    return false
}
```

### Why confirm instead of auto-stop

- "Wait" is ambiguous — could mean "pause" or "I have more to say"
- "Stop" during a 10-minute coding task could lose significant work
- A button takes 0.5s to tap and removes all ambiguity
- The non-destructive default (keep going on timeout) means accidental stop words don't kill work
- This is consistent with the interrupt button pattern above

This is a transport/runtime confirmation, not a model-only etiquette rule. The governor may still ask for confirmation in language, but stop-word handling while busy should remain machine-enforced.

### Config

```toml
[telegram]
stop_word_confirm = true          # Show confirmation button on stop words (vs auto-stop)
stop_word_timeout = "15s"         # Auto-continue after this timeout
```

### Tests

- **TestStopWordDetection**: "wait", "Stop", "CANCEL", "nvm" → detected. "waiting", "I'm stopping by" → not detected.
- **TestStopWordConfirmStop**: Send "wait" while busy → tap Yes → turn cancelled.
- **TestStopWordConfirmContinue**: Send "wait" while busy → tap No → message queued.
- **TestStopWordTimeout**: Send "stop" while busy → no tap → auto-continue.
- **TestStopWordWithContent**: Send "wait, do X instead" → tap Yes → turn cancelled, full message routed as new turn.
- **TestStopWordOnlyDiscard**: Send "stop" (nothing else) → tap Yes → turn cancelled, no new turn started.
- **TestStopWordNotBusy**: Send "wait" when agent is idle → no button, treated as normal message.

## Message Edit — Session Fork

When a user edits a previous message, everything after that point was based on stale input. We treat this as a **fork point**: rewind the session to the edited message and replay from there.

### Flow

```
1. User edits message N (Telegram sends edited_message update)
2. Aphelion identifies the message in session history by Telegram message_id
3. Cancel any in-progress turn for this session
4. Fork the session:
   a. Find the turn_index of the edited message in the DB
   b. Mark all messages AFTER that turn_index as compacted=1 (soft delete)
   c. Update the edited message's content in the DB to the new text
   d. Session now looks like history ended at the edited message
5. Delete stale Telegram messages:
   a. We track outbound message IDs (bot responses, tool progress) per turn
   b. Delete all bot messages that came after the edited message
   c. This visually "rewinds" the chat for the user
6. Process the edited message as a new turn:
   a. The LLM sees the corrected history and responds fresh
   b. New response appears right after the edited message in the chat
```

### Message ID Tracking

To delete stale bot messages, we need to track which Telegram message IDs we sent per turn:

```go
// In the messages table, add outbound tracking
type OutboundRecord struct {
    TurnIndex    int
    TelegramMsgID int64   // The message_id Telegram returned from sendMessage/editMessage
    Type         string   // "response", "progress", "streaming", "keyboard"
}
```

```sql
CREATE TABLE outbound_messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id         INTEGER NOT NULL,
    user_id         INTEGER NOT NULL DEFAULT 0,
    turn_index      INTEGER NOT NULL,
    telegram_msg_id INTEGER NOT NULL,
    msg_type        TEXT NOT NULL,  -- 'response', 'progress', 'streaming', 'keyboard'
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
);

CREATE INDEX idx_outbound_session ON outbound_messages(chat_id, user_id, turn_index);
```

### Implementation

```go
func (h *EditHandler) OnMessageEdit(ctx context.Context, edit *EditedMessage) {
    key := session.SessionKey{ChatID: edit.Chat.ID, UserID: resolveUserID(edit)}
    
    // 1. Cancel any in-progress turn
    if cancel, ok := h.router.ActiveCancel(key); ok {
        cancel()
    }
    
    // 2. Find the original message in session history
    turnIndex, err := h.store.FindTurnByTelegramMsgID(key, edit.MessageID)
    if err != nil {
        // Message not found in history (too old, or not tracked)
        // Fall back: treat as a new message
        h.router.Route(ctx, normalizeEditAsNew(edit))
        return
    }
    
    // 3. Fork: mark everything after turnIndex as compacted
    err = h.store.ForkAt(key, turnIndex, edit.Text)
    
    // 4. Delete stale bot messages from Telegram
    staleIDs, _ := h.store.OutboundAfterTurn(key, turnIndex)
    for _, msgID := range staleIDs {
        h.sender.DeleteMessage(ctx, edit.Chat.ID, msgID)
    }
    
    // 5. Process the edited message as a fresh turn
    h.router.Route(ctx, normalizeEditAsNew(edit))
}
```

### Store methods for fork

```go
// ForkAt marks all messages after turnIndex as compacted and updates the message at turnIndex
func (s *SQLiteStore) ForkAt(key SessionKey, turnIndex int, newContent string) error {
    return s.inTransaction(func(tx *sql.Tx) error {
        // Mark subsequent messages as compacted (soft delete)
        _, err := tx.Exec(
            `UPDATE messages SET compacted = 1 
             WHERE chat_id = ? AND user_id = ? AND turn_index > ?`,
            key.ChatID, key.UserID, turnIndex,
        )
        if err != nil {
            return err
        }
        
        // Update the edited message content
        _, err = tx.Exec(
            `UPDATE messages SET content = ?, content_chars = ? 
             WHERE chat_id = ? AND user_id = ? AND turn_index = ? AND role = 'user'`,
            newContent, len(newContent), key.ChatID, key.UserID, turnIndex,
        )
        return err
    })
}

// OutboundAfterTurn returns Telegram message IDs sent after a given turn
func (s *SQLiteStore) OutboundAfterTurn(key SessionKey, turnIndex int) ([]int64, error) {
    rows, err := s.db.Query(
        `SELECT telegram_msg_id FROM outbound_messages 
         WHERE chat_id = ? AND user_id = ? AND turn_index > ? 
         ORDER BY telegram_msg_id`,
        key.ChatID, key.UserID, turnIndex,
    )
    // ... collect and return IDs
}
```

### Edge cases

- **Edit a very old message**: If the turn_index is far back, this effectively rewinds a lot of history. The compacted messages are preserved on disk for audit. The visual cleanup deletes bot messages (Telegram allows deleting bot messages within 48 hours).
- **Edit while agent is mid-turn on that message**: Cancel first, then fork. The cancel is context-based and clean.
- **Edit a message we don't track**: Fall back to treating the edited text as a new message.
- **Multiple rapid edits**: Each edit forks from the latest edit position. The intermediate forks are no-ops if no turns ran between them.

### Telegram limitations

- `deleteMessage` only works for messages sent within the last 48 hours
- In groups, bots can only delete their own messages (not user messages)
- `edited_message` updates include the full new text but not a diff

### Config

```toml
[telegram]
# Message edit behavior
edit_fork = true                  # Fork session on message edit (vs ignore edits)
edit_cleanup = true               # Delete stale bot messages after fork
```

### Tests

- **TestEditForkSession**: Edit message at turn 5 of 10 → turns 6-10 marked compacted, session continues from turn 5.
- **TestEditUpdatesContent**: After fork, the edited message has new content in DB.
- **TestEditDeletesStaleMessages**: Bot messages after the edit point → deleteMessage called for each.
- **TestEditCancelsActiveTurn**: Edit during active turn → turn cancelled before fork.
- **TestEditUnknownMessage**: Edit a message not in history → treated as new message.
- **TestEditOldMessage48h**: Edit message older than 48h → fork works but cleanup skips (can't delete).
- **TestEditPreservesHistory**: After fork, compacted messages still exist in DB (audit trail).

## Config (in config.md)

```toml
[telegram]
bot_token = ""
allowed_chats = []        # Empty = allow all
poll_timeout = 30         # Long-poll timeout seconds
max_message_length = 4096
parse_mode = "MarkdownV2"
```

`allowed_chats` is only a coarse pre-filter. It is not a replacement for principal resolution from config.

## Module Structure

```
telegram/
├── bot.go        # Poller, Client (getUpdates, sendMessage, etc.)
├── format.go     # formatMarkdownV2, stripMarkdown, splitMessage, escapeMarkdownV2
├── types.go      # Update, Message, User, Chat, etc.
└── normalize.go  # normalizeUpdate, extractMedia, shouldRespond, buildDisplayName
```

## Tests

### Polling

- **TestGetUpdates**: Mock HTTP server returns 3 updates → all 3 received, offset advanced.
- **TestGetUpdatesEmpty**: Mock returns empty array → no error, offset unchanged.
- **TestGetUpdatesError**: Mock returns 500 → error logged, retry after backoff.
- **TestGetUpdatesContextCancel**: Cancel context → poller exits cleanly.

### Normalization

- **TestNormalizeTextMessage**: Message with text → InboundMessage with correct fields.
- **TestNormalizePhotoMessage**: Message with photo array → largest photo extracted as Media.
- **TestNormalizeReply**: Message replying to another → ReplyTo set.
- **TestNormalizeCaptionFallback**: Media message with caption, no text → Text = caption.
- **TestNormalizeNoMessage**: Update with no message field → returns nil.

### Group behavior

- **TestShouldRespondDMAdmin**: Configured admin private chat → routed.
- **TestShouldRespondDMApprovedUser**: Configured approved-user private chat → routed.
- **TestShouldRespondDMUnknownDenied**: Unknown private chat → no session created, denial notice optionally sent.
- **TestShouldRespondMention**: Group message with @botname → true.
- **TestShouldRespondReply**: Group message replying to bot → true.
- **TestShouldRespondIgnore**: Group message, no mention, no reply → false.
- **TestSenderPrefixShared**: Shared group scope → message prefixed with sender name.
- **TestSenderPrefixPerUser**: Per-user group scope → no prefix.

### Review delivery

- **TestDeliverReviewDigest**: Pending review digest is sent to the configured admin DM.
- **TestReviewDigestLabeling**: Delivered digest text includes source user and turn range.

### MarkdownV2

- **TestFormatBold**: `**bold**` → `*bold*`.
- **TestFormatItalic**: `*italic*` → `_italic_`.
- **TestFormatCode**: `` `code` `` → `` `code` `` (unchanged but content escaped).
- **TestFormatCodeBlock**: Triple backtick blocks → preserved with language tag.
- **TestFormatLink**: `[text](url)` → `[text](url)` with proper escaping.
- **TestFormatEscapeSpecialChars**: Text with `.`, `!`, `(` → properly escaped.
- **TestFormatFallback**: Invalid MarkdownV2 → strip to plain text.

### Message splitting

- **TestSplitShort**: Message under 4096 → single chunk.
- **TestSplitLong**: Message over 4096 → split at paragraph boundary.
- **TestSplitNoBreak**: Long message with no good split point → hard split at maxLen.
- **TestSplitPreservesCodeBlock**: Code block spanning split point → split before the block.

### Sending

- **TestSendText**: Mock HTTP server → correct JSON body with chat_id, text, parse_mode.
- **TestSendTextReply**: With ReplyTo → reply_parameters included.
- **TestSendTextFallback**: MarkdownV2 fails → retried as plain text.
- **TestSendTyping**: sendChatAction called with "typing".
- **TestSetReaction**: setMessageReaction called with correct emoji.

### Streaming

- **TestStreamFirstChunk**: First chunk → sendMessage called (not editMessageText).
- **TestStreamEdit**: Multiple chunks → editMessageText called with accumulated text + cursor.
- **TestStreamFinish**: Finish() → final edit without cursor.
- **TestStreamOverflow**: Text exceeds 4096 → current message finalized, new message started.
- **TestStreamEditFallback**: editMessageText fails → falls back to new sendMessage.

### Tool progress

- **TestToolProgressAll**: Mode=all, 3 tool calls → 3 lines in progress message.
- **TestToolProgressNew**: Mode=new, same tool 3x → only 1 line (dedup).
- **TestToolProgressOff**: Mode=off → no progress message sent.
- **TestToolProgressEmoji**: Each tool name maps to correct emoji.
- **TestToolProgressCleanup**: cleanup=true → progress message deleted after turn.

### File download

- **TestDownloadFile**: Mock getFile + file download → bytes match.
- **TestDownloadFileTooLarge**: File over 20MB → error.
