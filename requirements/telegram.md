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

For outbound DM turns, Telegram is the visible surface of the face layer:

1. user message enters raw
2. governor decides
3. face renders the visible reply
4. Telegram sends the rendered reply

## DM Admission

Telegram is the ingress path for all v0 conversations, so admission starts here.

For private chats:

- if the sender resolves to a configured principal, route the message into the DM session
- if the sender does not resolve to a configured principal, do not create a session and optionally send a fixed denial response

In v0, admission is config-owned. The Telegram layer should treat it as explicit principal policy, not as “all private chats are valid.”

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

## Attachment Processing Pipeline

When a user sends media, it needs to be converted into something the LLM can use. Each media type has a different pipeline.

### Photos → Vision input

Photos are passed directly to the LLM as image content blocks (Anthropic, Gemini, OpenAI all support this).

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
        // Extract text via pdftotext (exec tool) or pass as document to Anthropic
        // Anthropic supports PDF input natively as base64 document blocks
        return &ContentBlock{
            Type: "document",
            Source: &DocumentSource{
                Type:      "base64",
                MediaType: "application/pdf",
                Data:      base64.StdEncoding.EncodeToString(data),
            },
        }, nil
        
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

### Voice messages → User chooses

Voice messages are ambiguous — could be speech to transcribe, a melody to analyze, or ambient audio. Show the user options:

```
User sends a voice message (15s, OGG/Opus)

Aphelion replies with inline keyboard:

"🎤 Voice message received (15s). How should I process it?"

[ 📝 Transcribe ]  [ 🎵 Analyze audio ]
[ 🤖 Let agent decide ]  [ ❌ Skip ]
```

**Button actions:**
- **📝 Transcribe** → Send to Whisper API, include transcript as text: `[Voice message, 15s]\nTranscript here`
- **🎵 Analyze audio** → Send to Gemini (supports audio input natively) with prompt "Describe this audio"
- **🤖 Let agent decide** → Download the file, tell the agent it has a voice message and let it choose tools
- **❌ Skip** → Include only metadata: `[Voice message, 15s, skipped]`

```go
func (h *MediaHandler) OnVoiceMessage(ctx context.Context, msg *core.InboundMessage, voice *Voice) {
    kbMsgID := h.sender.SendInlineKeyboard(ctx, msg.ChatID,
        fmt.Sprintf("🎤 Voice message received (%ds). How should I process it?", voice.Duration),
        []InlineButton{
            {Text: "📝 Transcribe", CallbackData: fmt.Sprintf("media:voice:transcribe:%s", voice.FileID)},
            {Text: "🎵 Analyze audio", CallbackData: fmt.Sprintf("media:voice:analyze:%s", voice.FileID)},
            {Text: "🤖 Let agent decide", CallbackData: fmt.Sprintf("media:voice:agent:%s", voice.FileID)},
            {Text: "❌ Skip", CallbackData: fmt.Sprintf("media:voice:skip:%s", voice.FileID)},
        },
        &msg.MessageID,
    )
    
    // Auto-timeout: default to "Let agent decide" after 30s
}
```

### Video → User chooses

Same pattern as voice:

```
"🎬 Video received (30s, 1920x1080). How should I process it?"

[ 🖼 Extract frames ]  [ 🎵 Analyze audio track ]
[ 🤖 Let agent decide ]  [ ❌ Skip ]
```

**Button actions:**
- **🖼 Extract frames** → Use ffmpeg to extract keyframes, pass as vision input
- **🎵 Analyze audio track** → Extract audio, send to Gemini or transcribe
- **🤖 Let agent decide** → Download, let agent choose
- **❌ Skip** → Metadata only: `[Video attached: 30s, 1920x1080]`

### Audio files → User chooses

```
"🎶 Audio file received (song.mp3, 3:42). How should I process it?"

[ 📝 Transcribe ]  [ 🎵 Analyze music ]
[ 🤖 Let agent decide ]  [ ❌ Skip ]
```

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
| Text document | Yes (read it) | Auto-process |
| PDF | Yes (read it) | Auto-process |
| Image-as-document | Yes (vision) | Auto-process |
| Sticker | Yes (emoji) | Auto-process |
| Voice | Ambiguous | Show buttons |
| Video | Ambiguous | Show buttons |
| Audio file | Ambiguous | Show buttons |
| Binary file | Ambiguous | Show buttons (or just metadata) |

Consistent with our design principle: **buttons over assumptions** when intent is ambiguous. Max 4 buttons per keyboard (2x2 grid fits well on mobile).
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
auto_transcribe_voice = true       # Automatically transcribe voice messages
auto_vision_photos = true          # Automatically include photos as vision input
pdf_as_document = true             # Send PDFs as Anthropic document blocks (vs text extraction)
```

### Tests

- **TestProcessPhoto**: Photo file_id → downloaded, base64 encoded, returned as image content block.
- **TestProcessDocumentText**: .py file → content returned as text block.
- **TestProcessDocumentPDF**: PDF → returned as document content block.
- **TestProcessDocumentImage**: .png sent as document → processed as image.
- **TestProcessDocumentBinary**: .zip → metadata-only text block.
- **TestVoiceShowsButtons**: Voice message → inline keyboard with 4 options.
- **TestVoiceTranscribe**: Tap Transcribe → Whisper called, transcript as text block.
- **TestVoiceAnalyze**: Tap Analyze → sent to Gemini, analysis as text block.
- **TestVoiceSkip**: Tap Skip → metadata-only text block.
- **TestVoiceTimeout**: No tap → defaults to "Let agent decide".
- **TestVideoShowsButtons**: Video → inline keyboard with 4 options.
- **TestAudioShowsButtons**: Audio file → inline keyboard with 4 options.
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
   
   [ 🛑 Stop & reassess ]  [ ⏳ Let it finish ]

3a. User taps "Stop & reassess":
    - Cancel the current turn's context (ctx.Cancel())
    - Agent turn exits cleanly (context cancellation is already handled)
    - Delete the inline keyboard message
    - Route the new message as a fresh turn
    - The new message includes context: "[Previous request was interrupted. Last tool output: ...]"

3b. User taps "Let it finish":
    - Queue the new message (latest-wins, cap 1)
    - Edit the keyboard message to: "Got it — I'll process your message next. ⏳"
    - After current turn completes, process the queued message as the next turn

3c. No tap (timeout 30s):
    - Default to "Let it finish" (queue the message)
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
            {Text: "🛑 Stop & reassess", CallbackData: "interrupt:stop"},
            {Text: "⏳ Let it finish", CallbackData: "interrupt:queue"},
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

### Router integration

The router's `Route()` method changes slightly:

```go
func (r *Router) Route(ctx context.Context, msg InboundMessage) {
    lock, queue, session := r.resolveSession(msg.ChatID)
    
    if !lock.TryLock() {
        // Session is busy — trigger interrupt handler instead of silent queue
        if r.interruptHandler != nil {
            r.interruptHandler.OnMessageWhileBusy(ctx, msg, r.activeCancels[msg.ChatID])
        } else {
            r.enqueueLatest(queue, msg) // Fallback: silent queue
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
- **TestInterruptQueue**: User sends message while busy → taps Let it finish → message queued, processed after current turn.
- **TestInterruptTimeout**: No tap → message auto-queued after 30s.
- **TestInterruptKeyboardSent**: Message while busy → inline keyboard reply sent to user.
- **TestInterruptCallbackAck**: Callback query → answerCallbackQuery sent (Telegram requires this).

## Stop Words — Confirmation, Not Assumption

OpenClaw silently aborts agent turns on trigger words like "wait", "stop", "cancel". This is fragile — "wait" often means "hold on, I'm adding more" not "abort everything."

Aphelion never silently acts on ambiguous intent. Instead, we show a confirmation button.

### Flow

```
1. User sends a message matching a stop pattern while agent is busy
   Patterns: "wait", "stop", "cancel", "nevermind", "nvm", "hold on", "abort"
   (case-insensitive, whole-message or message starts with)

2. Aphelion immediately replies with an inline keyboard:
   
   "🛑 Stop the current task?"
   
   [ Yes, stop ]  [ No, keep going ]
   
   (And the user's message is preserved — it might contain follow-up context)

3a. User taps "Yes, stop":
    - Cancel current turn
    - Delete the keyboard message
    - If the user's message was ONLY a stop word ("wait", "stop"), discard it
    - If the user's message had additional content ("wait, actually do X instead"),
      route the full message as a new turn

3b. User taps "No, keep going":
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
