# Sessions — Conversation State, Compaction & Context Management

## Overview

A session is a conversation between one Telegram chat and the agent. It holds message history, system prompt state, and cache metadata. Sessions are persisted in SQLite and loaded into memory for each turn.

## Session Identity

Sessions are keyed by **Telegram chat ID** (int64). One session per chat.

- DMs: one session per user.
- Groups: one session per group chat. All users in the group share the session.
- If we later need per-user sessions in groups, we can extend the key to `chat_id:user_id`. Not needed for v1.

## Session State

```go
type Session struct {
    ChatID        int64
    Messages      []Message       // Full conversation history
    SystemPrompt  string          // Snapshot of the assembled system prompt
    CreatedAt     time.Time
    UpdatedAt     time.Time
    TurnCount     int
    
    // Cache tracking
    CacheState    CacheState
    
    // Compaction
    CompactionLog []CompactionEntry  // Record of what was compacted and when
    
    // Token accounting
    TotalInputTokens   int64
    TotalOutputTokens  int64
    TotalCacheRead     int64
    TotalCacheWrite    int64
}

type CacheState struct {
    LastWriteBlock    int       // Block index of the last cache write
    BlocksSinceWrite  int       // Blocks accumulated since last write
    LastWriteTime     time.Time // When the last cache write happened
    HitRate           float64   // Running cache hit rate
    ConsecutiveMisses int       // For adaptive strategy switching
}

type CompactionEntry struct {
    Timestamp     time.Time
    TurnsBefore   int    // Number of turns before compaction
    TurnsAfter    int    // Number of turns preserved
    TokensBefore  int    // Estimated tokens before
    TokensAfter   int    // Estimated tokens after
    Summary       string // The compaction summary (if summarize strategy)
}
```

## SQLite Schema

```sql
CREATE TABLE sessions (
    chat_id       INTEGER PRIMARY KEY,
    system_prompt TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    turn_count    INTEGER NOT NULL DEFAULT 0,
    -- Cache state
    cache_last_write_block  INTEGER NOT NULL DEFAULT 0,
    cache_blocks_since      INTEGER NOT NULL DEFAULT 0,
    cache_last_write_time   TEXT,
    cache_hit_rate          REAL NOT NULL DEFAULT 0.0,
    -- Token totals
    total_input_tokens    INTEGER NOT NULL DEFAULT 0,
    total_output_tokens   INTEGER NOT NULL DEFAULT 0,
    total_cache_read      INTEGER NOT NULL DEFAULT 0,
    total_cache_write     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    INTEGER NOT NULL REFERENCES sessions(chat_id),
    role       TEXT NOT NULL,  -- "user", "assistant", "tool"
    content    TEXT NOT NULL,  -- JSON-encoded content blocks
    tool_calls TEXT,           -- JSON-encoded tool calls (assistant messages)
    tool_id    TEXT,           -- Tool call ID (tool result messages)
    thinking   TEXT,           -- Extended thinking content (assistant messages)
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    turn_index INTEGER NOT NULL,
    -- Pruning metadata
    pruned     INTEGER NOT NULL DEFAULT 0,  -- 0=full, 1=soft-trimmed, 2=hard-cleared
    CONSTRAINT fk_chat FOREIGN KEY (chat_id) REFERENCES sessions(chat_id) ON DELETE CASCADE
);

CREATE INDEX idx_messages_chat ON messages(chat_id, turn_index);

CREATE TABLE compaction_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    INTEGER NOT NULL REFERENCES sessions(chat_id),
    timestamp  TEXT NOT NULL DEFAULT (datetime('now')),
    turns_before  INTEGER,
    turns_after   INTEGER,
    tokens_before INTEGER,
    tokens_after  INTEGER,
    summary    TEXT,
    CONSTRAINT fk_chat_compact FOREIGN KEY (chat_id) REFERENCES sessions(chat_id) ON DELETE CASCADE
);
```

### Why this schema

- **Messages in a separate table**: Allows efficient range queries (load last N turns), selective pruning (update `pruned` flag without rewriting), and fast append (INSERT, not UPDATE of a growing JSON blob).
- **turn_index**: Monotonically increasing per session. Used for pruning age calculations and compaction boundaries.
- **pruned flag**: 0=full content, 1=soft-trimmed (head+tail), 2=hard-cleared (placeholder only). Pruning is applied at query time — the original content is preserved in the `content` column, and the pruned version is computed when assembling the prompt.
- **No message content in the sessions table**: Avoids the "one giant row" problem that SQLite handles poorly.

## Session Lifecycle

### Load

```go
func (s *Store) Load(chatID int64) (*Session, error) {
    // 1. SELECT from sessions WHERE chat_id = ?
    // 2. If not found, create new session
    // 3. SELECT messages WHERE chat_id = ? ORDER BY turn_index
    // 4. Assemble Session struct
    // 5. Return
}
```

Load is called at the start of every turn by the router.

### Save (after each turn)

```go
func (s *Store) Save(session *Session, newMessages []Message, usage TokenUsage) error {
    // In a single transaction:
    // 1. INSERT new messages
    // 2. UPDATE sessions SET updated_at, turn_count, cache state, token totals
    // 3. COMMIT
}
```

Save is called after each turn completes. Only new messages are inserted — we never rewrite existing messages (append-only).

### Delete / Expire

```go
func (s *Store) ExpireIdle(maxIdle time.Duration) (int, error) {
    // DELETE FROM sessions WHERE updated_at < datetime('now', '-' || maxIdle)
    // CASCADE deletes messages and compaction_log
    // Return count of expired sessions
}
```

Called periodically (e.g., on heartbeat) to clean up idle sessions.

## Context Assembly

Every turn, the full prompt is assembled from the session state:

```
1. System prompt (stable prefix):
   a. Tool definitions (sorted, cache_control breakpoint #1)
   b. Bootstrap files — SOUL.md, IDENTITY.md, USER.md, AGENTS.md, TOOLS.md (cache_control breakpoint #2)

2. System prompt (dynamic suffix):
   a. MEMORY.md, HEARTBEAT.md, daily notes
   b. Runtime metadata (timestamp, etc.)

3. Messages:
   a. Load all messages from SQLite
   b. Apply pruning:
      - Messages older than pruning_soft_age turns: soft-trim tool results
      - Messages older than pruning_hard_age turns: hard-clear tool results
      - Non-tool messages are never pruned
   c. Check total token estimate
   d. If over max_context_ratio * context_window → trigger compaction
   e. Automatic cache_control on the request (breakpoint auto-advances)

4. New user message appended at the end
```

### System Prompt Caching

The system prompt is split into two parts:

**Stable prefix** (cached):
- Tool definitions
- Bootstrap files (SOUL.md, IDENTITY.md, USER.md, AGENTS.md, TOOLS.md)
- These rarely change. Explicit `cache_control` breakpoints here.

**Dynamic suffix** (not cached):
- MEMORY.md, HEARTBEAT.md
- Daily notes (memory/YYYY-MM-DD.md for today + yesterday)
- Timestamp, runtime info
- These change every turn or every few minutes. Placed AFTER the cache breakpoints.

The system prompt is re-rendered only if bootstrap files change (detected via content hash). Otherwise, the exact bytes from the previous turn are reused to preserve the cache prefix.

### System Prompt Fingerprinting

```go
func fingerprintSystemPrompt(blocks []ContentBlock) string {
    h := sha256.New()
    for _, b := range blocks {
        // Normalize: trim trailing whitespace, LF-only, collapse blank lines
        normalized := normalizeText(b.Text)
        h.Write([]byte(normalized))
    }
    return hex.EncodeToString(h.Sum(nil))
}
```

If the fingerprint matches the previous turn's, the system prompt bytes are reused exactly. No re-rendering, no cache busting.

## Compaction

Compaction is triggered when the assembled prompt (system prompt + messages + new user message) exceeds `max_context_ratio * context_window` tokens.

### Strategy: Summarize (default)

```
1. Select messages from the oldest up to the compaction boundary
   (keep the most recent messages that fit in compaction_ratio * context_window)
2. Send those messages to the LLM with a compaction prompt:
   "Summarize this conversation concisely, preserving key decisions, 
    facts, tool outputs, and context the assistant needs to continue."
3. Replace the selected messages with a single "system" message containing the summary
4. Log the compaction in compaction_log
5. Delete the replaced messages from SQLite
```

### Strategy: Truncate (simple)

Just drop the oldest messages. No LLM call. Cheaper but loses context.

### Cache-Aware Compaction

Compaction invalidates the message-history cache (the message content changes). To minimize cost:

1. **Time it with TTL expiry.** If we know the cache will expire in the next few minutes anyway, compact now rather than paying for a cache write that'll be invalidated.
2. **Preserve the system prompt prefix.** Compaction only touches messages, not the system prompt. The system prompt cache breakpoints remain valid after compaction.
3. **Check min_cache_tokens.** After compaction, the remaining messages must still exceed `min_cache_tokens` for caching to kick in. If not, warn (but don't pad — the system prompt alone should exceed min_cache_tokens).
4. **Update cache state.** Reset `blocks_since_write` after compaction since the message structure has changed.

### Compaction Budget

The compaction LLM call itself counts against cost. Use a cheaper model if configured:

```toml
[sessions]
compaction_model = ""  # Empty = use default provider. Or "anthropic:claude-haiku-4-5" for cheap compaction.
```

## Pruning (In-Memory Only)

Pruning is applied when assembling the prompt, NOT when saving to SQLite. The original messages are always preserved on disk.

### Soft-trim (pruning_soft_age)

For tool results older than `pruning_soft_age` turns:
```
Original: "total 42\ndrwxr-xr-x  5 user user 4096 Apr  8 ...\n[500 more lines]"
Trimmed:  "total 42\ndrwxr-xr-x  5 user user 4096 Apr  8 ...\n... [498 lines trimmed] ...\n-rw-r--r--  1 user user  231 Apr  8 08:00 README.md"
```

Keep first 3 lines + last 2 lines. This preserves enough context for the LLM to understand what the tool did without paying for the full output.

### Hard-clear (pruning_hard_age)

For tool results older than `pruning_hard_age` turns:
```
Original: [any tool result]
Cleared:  "[tool output from turn N, trimmed for context]"
```

Single-line placeholder. The LLM knows a tool was called and roughly when, but not the output.

### Non-tool messages are never pruned

User messages and assistant text responses are always sent in full. They're the conversation — pruning them would lose context that can't be recovered.

## Session Store Interface

```go
type Store interface {
    Load(chatID int64) (*Session, error)
    Save(session *Session, newMessages []Message, usage TokenUsage) error
    UpdateCacheState(chatID int64, state CacheState) error
    Compact(chatID int64, summary string, keepFromTurn int) error
    ExpireIdle(maxIdle time.Duration) (int, error)
    Close() error
}
```

Implementation: `session/store.go` with `mattn/go-sqlite3`.

Single writer goroutine pattern: all writes go through a channel to a dedicated goroutine. Reads can happen concurrently (SQLite WAL mode).

```go
type SQLiteStore struct {
    db      *sql.DB
    writeCh chan writeOp
}

func (s *SQLiteStore) init() {
    // Enable WAL mode for concurrent reads
    s.db.Exec("PRAGMA journal_mode=WAL")
    s.db.Exec("PRAGMA synchronous=NORMAL")
    // Foreign keys
    s.db.Exec("PRAGMA foreign_keys=ON")
    
    // Start single writer goroutine
    go s.writeLoop()
}
```

## Config (in config.md)

```toml
[sessions]
db_path = "~/.config/aphelion/sessions.db"
max_context_ratio = 0.92
compaction_ratio = 0.65
compaction_strategy = "summarize"   # "summarize" | "truncate"
compaction_model = ""               # Empty = default. Or specific model for cheap compaction.
idle_expiry = "24h"
```

Pruning config lives under the Anthropic provider section (since it's cache-aware):
```toml
[providers.anthropic]
cache_ttl_pruning = true
pruning_soft_age = 10
pruning_hard_age = 20
```

## Tests

### Store

- **TestCreateSession**: Load nonexistent chat_id → new session created with defaults.
- **TestSaveAndLoad**: Save messages → load → messages match.
- **TestAppendOnly**: Save 3 messages, then save 2 more → load returns all 5 in order.
- **TestTurnIndex**: Messages have monotonically increasing turn_index.
- **TestCacheStateUpdate**: Update cache state → load → state matches.
- **TestExpireIdle**: Create session, advance time past idle_expiry → expired and deleted.
- **TestExpireKeepsActive**: Create two sessions, advance time → only idle one expires.
- **TestCascadeDelete**: Delete session → messages and compaction_log also deleted.
- **TestConcurrentReads**: 10 goroutines read same session simultaneously → no errors (WAL mode).
- **TestWALMode**: After init, PRAGMA journal_mode returns "wal".

### Context Assembly

- **TestAssembleBasic**: System prompt + 3 messages → assembled correctly in order.
- **TestPruningSoft**: Tool result at turn 0, current turn 15 (soft_age=10) → result is soft-trimmed.
- **TestPruningHard**: Tool result at turn 0, current turn 25 (hard_age=20) → result is hard-cleared.
- **TestPruningPreservesNonTool**: User message at turn 0, current turn 25 → NOT pruned.
- **TestPruningInMemoryOnly**: After assembly with pruning, reload from DB → original content intact.
- **TestSystemPromptFingerprint**: Same bootstrap files → same fingerprint. Modified file → different fingerprint.
- **TestSystemPromptReuse**: Fingerprint unchanged → exact same bytes used (no re-render).
- **TestDynamicSuffixAfterCacheBoundary**: MEMORY.md content appears after the last explicit cache breakpoint.

### Compaction

- **TestCompactionTrigger**: Messages exceed max_context_ratio → compaction triggered.
- **TestCompactionSummarize**: After compaction, oldest messages replaced with summary. Recent messages preserved.
- **TestCompactionTruncate**: Strategy=truncate → oldest messages dropped, no LLM call.
- **TestCompactionLog**: After compaction, compaction_log has entry with correct token counts.
- **TestCompactionPreservesSystemPrompt**: After compaction, system prompt cache breakpoints are still valid.
- **TestCompactionCacheStateReset**: After compaction, blocks_since_write is reset to 0.
- **TestCompactionMinCacheTokens**: After compaction, remaining tokens still exceed min_cache_tokens (or warning logged).

### Integration

- **TestFullSessionLifecycle**: Create session → 50 turns → pruning kicks in → compaction triggers → session continues → expire.
