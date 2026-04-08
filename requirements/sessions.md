# Sessions — Conversation State, Compaction & Context Management

## Overview

A session is a conversation between one Telegram chat and the agent. It holds message history, system prompt state, and cache metadata. Sessions are persisted in SQLite and loaded into memory for each turn.

## Session Identity

Sessions are keyed by a composite of **chat ID + scope**.

### DMs
One session per user. Key: `chat_id`.

### Groups
Configurable via `session.group_scope`:

- `"shared"` — One session per group. All users share history. The LLM sees `[sender_name]: message` prefixes to distinguish speakers. Simpler, cheaper (one context window), but messages from all users accumulate in one history.
- `"per_user"` (default) — One session per user per group. Key: `chat_id:user_id`. Each user gets their own conversation history with the agent, even within the same group. This is what Hermes does by default (`group_sessions_per_user=True`) and avoids context pollution from unrelated users.

Both OpenClaw and Hermes support per-user group sessions. OpenClaw calls it `dmScope: "per-channel-peer"`. We default to `per_user` because:
- It prevents one user's long tool-heavy session from blowing up context for everyone
- It avoids exposing one user's conversation to another in the same group
- It matches the mental model: talking to the bot in a group feels like a DM that happens to be visible

In shared mode, the system prompt notes this is a multi-user context and does NOT pin a single user name (which would bust the prompt cache — the name changes per turn). Instead, each user message is prefixed with the sender name by the Telegram handler.

### Group mention behavior
In groups, the agent only responds when mentioned (or replied to). This is hardcoded for v1 — no `requireMention` config needed since we only have one channel.

## Session State

```go
type Session struct {
    ChatID        int64
    UserID        int64           // 0 for shared group sessions
    Messages      []Message       // Full conversation history
    SystemPrompt  string          // Snapshot of the assembled system prompt (for cache reuse)
    CreatedAt     time.Time
    UpdatedAt     time.Time
    TurnCount     int
    
    // Cache tracking
    CacheState    CacheState
    
    // Compaction
    CompactionLog []CompactionEntry  // Record of what was compacted and when
    
    // Token accounting (cumulative across all turns)
    TotalInputTokens   int64
    TotalOutputTokens  int64
    TotalCacheRead     int64
    TotalCacheWrite    int64
    
    // Provider state
    LastProvider  string          // Which provider was used last (for failover tracking)
    LastModel     string          // Which model was used last
    
    // Agent state
    ActiveToolCalls int           // Tool calls in progress (for crash recovery)
    LastError       string        // Last error message (for debugging)
    
    // Metadata
    ChatType      string          // "dm" or "group"
    ChatTitle     string          // Group title (for logging/display)
    UserName      string          // Sender display name
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
-- Schema version tracking for migrations
CREATE TABLE schema_version (
    version    INTEGER NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO schema_version (version) VALUES (1);

CREATE TABLE sessions (
    -- Composite key: chat_id + user_id (user_id=0 for shared group sessions)
    chat_id       INTEGER NOT NULL,
    user_id       INTEGER NOT NULL DEFAULT 0,
    system_prompt TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    turn_count    INTEGER NOT NULL DEFAULT 0,
    -- Chat metadata
    chat_type     TEXT NOT NULL DEFAULT 'dm',  -- 'dm', 'group'
    chat_title    TEXT,
    user_name     TEXT,
    -- Cache state
    cache_last_write_block  INTEGER NOT NULL DEFAULT 0,
    cache_blocks_since      INTEGER NOT NULL DEFAULT 0,
    cache_last_write_time   TEXT,
    cache_hit_rate          REAL NOT NULL DEFAULT 0.0,
    -- Token totals (cumulative)
    total_input_tokens    INTEGER NOT NULL DEFAULT 0,
    total_output_tokens   INTEGER NOT NULL DEFAULT 0,
    total_cache_read      INTEGER NOT NULL DEFAULT 0,
    total_cache_write     INTEGER NOT NULL DEFAULT 0,
    -- Provider state
    last_provider TEXT,
    last_model    TEXT,
    -- Error tracking
    last_error    TEXT,
    PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    INTEGER NOT NULL,
    user_id    INTEGER NOT NULL DEFAULT 0,
    role       TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool')),
    content    TEXT NOT NULL,  -- JSON-encoded content blocks
    tool_calls TEXT,           -- JSON-encoded tool calls (assistant only)
    tool_id    TEXT,           -- Tool call ID (tool result messages)
    tool_name  TEXT,           -- Tool name (tool result messages, for pruning decisions)
    thinking   TEXT,           -- Extended thinking content (assistant only)
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    turn_index INTEGER NOT NULL,
    -- Content size tracking (for fast token estimation without parsing JSON)
    content_chars INTEGER NOT NULL DEFAULT 0,
    -- Pruning metadata (applied in-memory, not mutated here)
    -- The pruned column records if this message was part of a compaction.
    compacted  INTEGER NOT NULL DEFAULT 0,  -- 0=active, 1=compacted (replaced by summary)
    FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
);

CREATE INDEX idx_messages_session ON messages(chat_id, user_id, turn_index);
CREATE INDEX idx_messages_active ON messages(chat_id, user_id, compacted, turn_index);

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

CREATE TABLE compaction_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    INTEGER NOT NULL,
    user_id    INTEGER NOT NULL DEFAULT 0,
    timestamp  TEXT NOT NULL DEFAULT (datetime('now')),
    turns_before  INTEGER,
    turns_after   INTEGER,
    tokens_before INTEGER,
    tokens_after  INTEGER,
    summary    TEXT,
    strategy   TEXT NOT NULL DEFAULT 'summarize',  -- 'summarize', 'truncate'
    FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
);
```

### Why this schema

- **Composite primary key** `(chat_id, user_id)`: Supports both DM sessions (user_id=0) and per-user group sessions naturally. No schema change needed when switching group scope.
- **Schema versioning**: `schema_version` table tracks migrations. Future schema changes are applied incrementally at startup.
- **Messages in a separate table**: Allows efficient range queries (load last N turns), fast append (INSERT, not UPDATE of a growing JSON blob), and index-based filtering.
- **turn_index**: Monotonically increasing per session. Used for pruning age calculations and compaction boundaries.
- **content_chars**: Stored alongside content for fast token estimation (`chars / 4`) without parsing JSON. Updated on INSERT.
- **compacted flag**: 0=active message, 1=replaced by compaction summary. Compacted messages are kept on disk (for audit) but excluded from context assembly via the `idx_messages_active` index.
- **CHECK constraint on role**: Prevents invalid role values at the DB level.
- **tool_name on tool results**: Enables pruning decisions based on tool type (e.g., never prune `memory_search` results, aggressively prune `exec` output).
- **Two indexes on messages**: `idx_messages_session` for full history load, `idx_messages_active` for fast context assembly (skips compacted messages).
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

**Why 75%, not 92%.** Models degrade as they approach context limits — increased hallucination, anxiety-like behavior, repetitive loops, and reduced instruction-following. The Mythos system card documents "context anxiety" explicitly. Compacting at 75% gives the model breathing room and keeps response quality high. The 65% compaction target means we drop ~10% of context, which is a modest compaction that preserves most history.

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
// SessionKey identifies a session uniquely.
type SessionKey struct {
    ChatID int64
    UserID int64 // 0 for shared group sessions or DMs
}

type Store interface {
    Load(key SessionKey) (*Session, error)
    Save(session *Session, newMessages []Message, usage TokenUsage) error
    UpdateCacheState(key SessionKey, state CacheState) error
    Compact(key SessionKey, summary string, keepFromTurn int) error
    ExpireIdle(maxIdle time.Duration) (int, error)
    ListActive(since time.Duration) ([]SessionKey, error)
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
max_context_ratio = 0.75            # Compact at 75%. Models degrade near limits.
compaction_ratio = 0.55             # Compact down to 55%. ~20% headroom before next compaction.
compaction_strategy = "summarize"    # "summarize" | "truncate"
compaction_model = ""                # Empty = default. Or "anthropic:claude-haiku-4-5" for cheap compaction.
idle_expiry = "24h"

[sessions.groups]
scope = "per_user"                   # "per_user" | "shared"
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

### Group sessions

- **TestPerUserGroupSession**: Two users in same group → separate sessions, separate histories.
- **TestSharedGroupSession**: scope=shared, two users → same session, messages prefixed with sender names.
- **TestGroupSessionKey**: Per-user key is `chat_id:user_id`, shared key is `chat_id:0`.

### Schema

- **TestSchemaVersion**: After init, schema_version table has version=1.
- **TestRoleConstraint**: INSERT message with role="invalid" → CHECK constraint error.
- **TestContentChars**: INSERT message → content_chars matches len(content).
- **TestCompactedIndex**: Compacted messages excluded from active index query.

### Integration

- **TestFullSessionLifecycle**: Create session → 50 turns → pruning kicks in → compaction triggers → session continues → expire.
