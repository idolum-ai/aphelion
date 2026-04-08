# Telegram — Bot API Integration

## Overview

Telegram is the sole channel for Aphelion. The telegram package handles long-polling for updates, normalizing them into `InboundMessage`, sending responses as `OutboundMessage`, and all Telegram-specific formatting.

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
    
    // Media extraction
    inbound.Media = extractMedia(msg)
    
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

### Media extraction

Extract photos, documents, audio, voice, video from the message:

```go
func extractMedia(msg *Message) []core.Media {
    var media []core.Media
    
    // Photos: take the largest size (last in array)
    if len(msg.Photo) > 0 {
        largest := msg.Photo[len(msg.Photo)-1]
        media = append(media, core.Media{
            Type: "photo",
            URL:  fileURL(largest.FileID), // resolved via getFile later
            // FileID stored for download
        })
    }
    
    if msg.Document != nil { /* ... */ }
    if msg.Audio != nil    { /* ... */ }
    if msg.Voice != nil    { /* ... */ }
    if msg.Video != nil    { /* ... */ }
    if msg.VideoNote != nil { /* ... */ }
    if msg.Sticker != nil  { /* ... */ }
    
    return media
}
```

Media files need a two-step download: `getFile` to get `file_path`, then `https://api.telegram.org/file/bot<token>/<file_path>` to download. We do this lazily — only when the agent actually needs the file content.

## Group Behavior

### Mention detection

In groups, the bot only responds when:
1. The message mentions the bot via `@botusername` (check `entities` for `mention` type)
2. The message is a reply to one of the bot's messages
3. The message contains a bot command (`/command@botusername`)

```go
func shouldRespond(msg *Message, botUsername string) bool {
    // Always respond in private chats
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

## Live Feedback — Streaming & Tool Progress

When the agent is working, the user should see what's happening — not just a typing indicator.

### Streaming text (edit-in-place)

As the LLM streams tokens, we progressively edit a single Telegram message:

1. **First chunk arrives** → `sendMessage` with initial text + cursor `▉`
2. **Every ~300ms** → `editMessageText` with accumulated text + cursor
3. **Stream complete** → final `editMessageText` without cursor

This gives a "typing in real time" feel. The cursor makes it obvious the message is still generating.

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

While the agent is in the tool-call loop, a separate progress message shows what tools are running:

```
🔍 web_fetch: "https://example.com"
💻 exec: "git status"
📝 write_file: "output.md"
```

Each new tool call adds a line. The message is edited in-place (one message, growing).

```go
type ToolProgressReporter struct {
    sender    *Sender
    chatID    int64
    messageID int64       // Progress message ID (0 = not sent yet)
    lines     []string    // Accumulated tool lines
    mode      string      // "all" | "new" | "off"
    lastTool  string      // For "new" mode dedup
}

func (r *ToolProgressReporter) OnToolStart(name string, argsPreview string) {
    if r.mode == "off" {
        return
    }
    if r.mode == "new" && name == r.lastTool {
        return
    }
    r.lastTool = name
    
    emoji := toolEmoji(name)
    line := fmt.Sprintf("%s %s", emoji, name)
    if argsPreview != "" {
        if len(argsPreview) > 40 {
            argsPreview = argsPreview[:37] + "..."
        }
        line += fmt.Sprintf(": \"%s\"", argsPreview)
    }
    r.lines = append(r.lines, line)
    
    text := strings.Join(r.lines, "\n")
    if r.messageID == 0 {
        r.messageID = r.sender.SendPlainText(r.chatID, text)
    } else {
        r.sender.EditPlainText(r.chatID, r.messageID, text)
    }
}

func toolEmoji(name string) string {
    switch name {
    case "exec":
        return "💻"
    case "read_file":
        return "📖"
    case "write_file":
        return "📝"
    case "web_fetch":
        return "🔍"
    case "memory_search":
        return "🧠"
    default:
        return "⚙️"
    }
}
```

**Modes** (configurable):
- `"all"` — Show every tool call (default)
- `"new"` — Only show when tool name changes (dedup consecutive same-tool calls)
- `"off"` — No tool progress messages

**Cleanup**: After the turn completes, optionally delete the progress message (or leave it for context). Configurable.

### Config

```toml
[telegram]
# ... existing fields ...

# Streaming
stream_edit_interval = "300ms"    # How often to edit the streaming message
stream_cursor = " \u2589"         # Cursor shown during streaming

# Tool progress
tool_progress = "all"             # "all" | "new" | "off"
tool_progress_cleanup = false     # Delete progress message after turn completes
```

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

## Config (in config.md)

```toml
[telegram]
bot_token = ""
allowed_chats = []        # Empty = allow all
poll_timeout = 30         # Long-poll timeout seconds
max_message_length = 4096
parse_mode = "MarkdownV2"
```

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

- **TestShouldRespondDM**: Private chat → always true.
- **TestShouldRespondMention**: Group message with @botname → true.
- **TestShouldRespondReply**: Group message replying to bot → true.
- **TestShouldRespondIgnore**: Group message, no mention, no reply → false.
- **TestSenderPrefixShared**: Shared group scope → message prefixed with sender name.
- **TestSenderPrefixPerUser**: Per-user group scope → no prefix.

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
