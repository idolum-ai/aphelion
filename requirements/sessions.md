# Sessions — Conversation State, Compaction & Context Management

## Overview

A session is a persisted conversation between one Telegram chat and the agent. It holds message history, assembled system prompt state, token/accounting metadata, and enough information to continue the conversation on the next turn.

This spec is staged. The immediate goal is a correct DM-only v0 with explicit admission control. After that, the next stage is approved multi-user DMs with hard isolation and digest forwarding into the admin DM. Group semantics, deeper context management, and provider-specific cache behavior remain part of the architecture, but they are explicitly deferred rather than folded into the first acceptance bar.

## Scope

### v0 required

- DM-only sessions
- Explicit approval before a DM can create or resume a session
- At least one admin principal
- SQLite-backed append-only message history
- Per-turn load, run, and save
- Stable Telegram DM session identity
- Session expiry support
- Prompt assembly from workspace files plus persisted active history

### v0.5: approved multi-user DMs

- `admin` and `approved_user` roles
- Hard isolation for non-admin writable state
- Read-only access to global persona and shared memory for non-admins
- Automatic digest forwarding from non-admin sessions into the admin DM
- Admin DM acts as the review surface; no separate UI required

### Deferred after v0.5

- Group sessions
- Shared vs per-user group scope
- Mention/reply gating in groups
- In-memory pruning policies
- Automatic compaction triggers and summary generation
- Cache-aware prompt fingerprinting and exact-byte prompt reuse
- Provider-specific cache heuristics coupled to session state

## Session Identity

### v0: DMs

For v0, the runtime is DM-only.

- One session per Telegram DM
- Key: `chat_id`
- Persist `user_id = 0`
- Persist `chat_type = "dm"`
- No session exists until the DM is approved

The store still uses a composite `(chat_id, user_id)` key so later group support does not require redesigning schema or APIs.

### v0 admission

Admission is required before a user can create or continue a session.

The implementation may begin with a simple allow-list, but the design target is explicit approval state:

- `pending`
- `approved`
- `banned`

For v0, admission happens only for DMs.

### v0.5 authority

After v0, approved users split into two roles:

- `admin`: trusted to mutate global state
- `approved_user`: trusted to talk to the system, but not to mutate global state directly

This distinction matters even in DM-only mode, because multiple approved DMs still interact with the same underlying system unless isolation is explicit.

### Deferred: Groups

Later group behavior should be configurable via `sessions.groups.scope`:

- `"shared"`: one session per group, key `chat_id:0`
- `"per_user"`: one session per user per group, key `chat_id:user_id`

In shared mode, user messages should be prefixed with the sender name by the channel adapter. The system prompt should not pin a single user name, because that would destabilize the prompt prefix.

### Deferred: Group mention behavior

When group support lands, the agent should only respond when mentioned or replied to. This is a channel-policy concern, not part of DM-only v0.

## Authority & Isolation

### v0

The simplest correct v0 is a single admin DM plus explicit admission for any future users. If only the admin is approved, the session may operate directly on the real workspace and shared memory.

### v0.5

Once more than one approved user exists, authority must split from admission.

- `admin` can write to the global workspace, global memory, and persona files
- `approved_user` can only write to isolated per-user state
- `approved_user` can read global persona and shared memory, but only as read-only prompt context
- `approved_user` cannot directly mutate shared memory, persona files, or the real workspace

The key rule is:

- **global mutation authority** belongs only to admin sessions
- **local work authority** belongs to each approved user's isolated session
- **cross-session knowledge transfer** happens only through bounded digests into the admin DM

## Session State

The session struct can be broader than v0 as long as the extra fields are honest architectural headroom rather than fake live behavior.

```go
type Session struct {
    ChatID       int64
    UserID       int64           // 0 in v0
    Role         string          // "admin" | "approved_user"
    Approved     bool
    Messages     []Message
    SystemPrompt string          // Snapshot of the assembled system prompt
    CreatedAt    time.Time
    UpdatedAt    time.Time
    TurnCount    int

    // Cache tracking
    CacheState CacheState

    // Compaction
    CompactionLog []CompactionEntry

    // Token accounting
    TotalInputTokens  int64
    TotalOutputTokens int64
    TotalCacheRead    int64
    TotalCacheWrite   int64

    // Provider state
    LastProvider string
    LastModel    string

    // Agent state
    ActiveToolCalls int
    LastError       string

    // Chat metadata
    ChatType  string // "dm" or "group"
    ChatTitle string
    UserName  string
    WorkspaceRoot string         // Real workspace for admin, isolated workspace for non-admin
}

type CacheState struct {
    LastWriteBlock    int
    BlocksSinceWrite  int
    LastWriteTime     time.Time
    HitRate           float64
    ConsecutiveMisses int
}

type CompactionEntry struct {
    Timestamp    time.Time
    TurnsBefore  int
    TurnsAfter   int
    TokensBefore int
    TokensAfter  int
    Summary      string
    Strategy     string // "summarize" or "truncate"
}
```

## SQLite Schema

```sql
CREATE TABLE schema_version (
    version    INTEGER NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO schema_version (version) VALUES (1);

CREATE TABLE sessions (
    chat_id       INTEGER NOT NULL,
    user_id       INTEGER NOT NULL DEFAULT 0,
    role          TEXT NOT NULL DEFAULT 'approved_user',
    approved      INTEGER NOT NULL DEFAULT 0,
    system_prompt TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    turn_count    INTEGER NOT NULL DEFAULT 0,
    chat_type     TEXT NOT NULL DEFAULT 'dm',
    chat_title    TEXT,
    user_name     TEXT,
    workspace_root TEXT,
    cache_last_write_block  INTEGER NOT NULL DEFAULT 0,
    cache_blocks_since      INTEGER NOT NULL DEFAULT 0,
    cache_last_write_time   TEXT,
    cache_hit_rate          REAL NOT NULL DEFAULT 0.0,
    total_input_tokens    INTEGER NOT NULL DEFAULT 0,
    total_output_tokens   INTEGER NOT NULL DEFAULT 0,
    total_cache_read      INTEGER NOT NULL DEFAULT 0,
    total_cache_write     INTEGER NOT NULL DEFAULT 0,
    last_provider TEXT,
    last_model    TEXT,
    active_tool_calls INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT,
    PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    INTEGER NOT NULL,
    user_id    INTEGER NOT NULL DEFAULT 0,
    role       TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool')),
    content    TEXT NOT NULL,
    tool_calls TEXT,
    tool_id    TEXT,
    tool_name  TEXT,
    thinking   TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    turn_index INTEGER NOT NULL,
    content_chars INTEGER NOT NULL DEFAULT 0,
    compacted  INTEGER NOT NULL DEFAULT 0,
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
    msg_type        TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
);

CREATE INDEX idx_outbound_session ON outbound_messages(chat_id, user_id, turn_index);

CREATE TABLE review_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    source_chat_id INTEGER NOT NULL,
    source_user_id INTEGER NOT NULL DEFAULT 0,
    source_role    TEXT NOT NULL,
    target_chat_id INTEGER NOT NULL,       -- admin DM chat_id
    turn_from      INTEGER,
    turn_to        INTEGER,
    summary        TEXT NOT NULL,
    delivered      INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_review_events_target ON review_events(target_chat_id, delivered, created_at);

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
    strategy   TEXT NOT NULL DEFAULT 'summarize',
    FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
);
```

### Why this schema

- **Composite primary key** `(chat_id, user_id)`: v0 only uses `user_id=0`, but the schema is already shaped for later group support.
- **Schema versioning**: future changes are applied incrementally at startup.
- **Messages in a separate table**: supports efficient load, append, and filtering without rewriting a giant blob.
- **turn_index**: provides a stable turn boundary for later pruning and compaction.
- **content_chars**: allows cheap token estimation.
- **compacted flag**: compacted messages remain on disk for audit but can be excluded from active prompt assembly.
- **outbound_messages**: keeps a durable mapping between agent turns and Telegram message IDs.
- **role + approved**: lets session admission and authority remain explicit rather than inferred from chat IDs alone.
- **workspace_root**: allows the runtime to bind a session to either the real workspace or an isolated per-user workspace.
- **review_events**: gives non-admin sessions a one-way path into the admin DM without merging raw history or raw tool output.

## Session Lifecycle

### Load

```go
func (s *Store) Load(key SessionKey) (*Session, error) {
    // 1. SELECT from sessions WHERE chat_id = ? AND user_id = ?
    // 2. If not found, create new session
    // 3. SELECT messages WHERE chat_id = ? AND user_id = ? ORDER BY turn_index, id
    // 4. Assemble Session struct
    // 5. Return
}
```

Load is called at the start of every turn.

### Save

```go
func (s *Store) Save(session *Session, newMessages []Message, usage TokenUsage) error {
    // In one transaction:
    // 1. INSERT new messages
    // 2. UPDATE sessions SET updated_at, turn_count, token totals, metadata
    // 3. COMMIT
}
```

Save is called after each turn completes. The persistent history is append-only. Existing rows are not rewritten during the normal turn path.

### Delete / Expire

```go
func (s *Store) ExpireIdle(maxIdle time.Duration) (int, error) {
    // DELETE idle sessions
    // CASCADE deletes messages, outbound_messages, and compaction_log
}
```

Expiry is useful in v0 even before compaction exists.

## Digest Forwarding

### v0.5 review membrane

The admin DM is the review surface. No separate UI is required.

Non-admin sessions stay isolated, but they periodically emit bounded digests into the admin DM:

1. compact or summarize a bounded slice of the non-admin session
2. store it as a `review_event`
3. forward it to the admin DM on a cadence or when the session goes idle
4. let the admin react naturally in the same DM

The digest itself is the membrane:

- raw session history does not cross the boundary
- raw tool output does not cross the boundary
- global state is not mutated by the non-admin session directly
- the admin can still ask the system to ban the user, delete the session, or forget the forwarded digest

No explicit promotion workflow is required. The digest is already a reduced, bounded transfer of context.

## Context Assembly

### v0 prompt assembly

Every turn:

1. Render the base system instruction.
2. Load workspace bootstrap files.
3. Load workspace dynamic files (`MEMORY.md`, `HEARTBEAT.md`, daily notes).
4. Load persisted messages for the session.
5. Exclude compacted messages from active history.
6. Append the new user message.
7. Run the model turn.
8. Persist new messages.

v0 does **not** require:

- pruning tool outputs
- automatic compaction triggers
- prompt fingerprinting
- exact-byte prompt reuse
- provider cache breakpoints in the session layer
- multi-user isolation
- digest forwarding

The only requirement is correctness: workspace files must be reflected on the next turn, and active persisted history must be replayed in order.

### v0.5 isolated prompt assembly

For non-admin sessions:

1. use the isolated workspace root
2. inject shared persona and shared memory as read-only context
3. inject per-user local memory as writable local context
4. never grant direct write access to global persona or shared memory

For the admin session:

1. use the real workspace root
2. receive forwarded digests from other sessions as labeled bot messages
3. treat those digests as normal conversational context inside the admin DM

### Deferred: Cache-aware prompt reuse

Later, prompt assembly should distinguish:

- **stable prefix**: bootstrap files and other rarely changing instructions
- **dynamic suffix**: `MEMORY.md`, `HEARTBEAT.md`, daily notes, runtime metadata

When this lands, unchanged stable content should be reused byte-for-byte to preserve provider cache prefixes.

## Compaction

Compaction is deferred after v0, but becomes more useful in v0.5 because digests are the mechanism for carrying bounded information from isolated non-admin sessions into the admin DM.

The design target is:

1. detect when assembled context exceeds `max_context_ratio * context_window`
2. summarize or truncate older turns
3. mark replaced messages as `compacted = 1`
4. insert a summary message at the compaction boundary
5. record the event in `compaction_log`

For non-admin sessions, the same summarization machinery can also produce `review_events` for the admin DM.

Compacted messages should remain on disk for audit. They should not be deleted as part of normal compaction.

## Pruning

Pruning is also deferred after v0.

When implemented, pruning is applied only in memory during prompt assembly:

- older tool results may be soft-trimmed
- older tool results may later be hard-cleared
- user and assistant conversational messages are never pruned

SQLite remains the source of truth for the full original transcript.

## Session Store Interface

### v0 required

```go
type SessionKey struct {
    ChatID int64
    UserID int64 // always 0 in v0
}

type Store interface {
    Load(key SessionKey) (*Session, error)
    Save(session *Session, newMessages []Message, usage TokenUsage) error
    ExpireIdle(maxIdle time.Duration) (int, error)
    Close() error
}
```

### Extended interface after v0

```go
type ExtendedStore interface {
    Store
    UpdateCacheState(key SessionKey, state CacheState) error
    Compact(key SessionKey, summary string, keepFromTurn int) error
    ListActive(since time.Duration) ([]SessionKey, error)
    EnqueueReviewEvent(event ReviewEvent) error
    PendingReviewEvents(targetChatID int64, limit int) ([]ReviewEvent, error)
    MarkReviewDelivered(ids []int64) error
}
```

Implementation lives in `session/store.go` with `mattn/go-sqlite3`.

Single-connection SQLite with WAL mode is sufficient for v0. A dedicated writer goroutine is an acceptable later refinement if write contention shows up, but it is not required for the first usable system.

## Config (see `config.md`)

### v0 required

```toml
[sessions]
db_path = "~/.config/aphelion/sessions.db"
idle_expiry = "24h"
```

### v0.5

```toml
[users]
admin_chat_id = 123456789
approved_chat_ids = [123456789, 222222222]

[reviews]
enabled = true
digest_every = "30m"
digest_on_idle = true
max_summary_chars = 1200

[sessions.isolation]
root = "~/.config/aphelion/workspaces"
shared_memory_dir = "~/.config/aphelion/memory/shared"
per_user_memory_dir = "~/.config/aphelion/memory/users"
```

### Deferred after v0.5

```toml
[sessions]
max_context_ratio = 0.75
compaction_ratio = 0.55
compaction_strategy = "summarize"
compaction_model = ""

[sessions.groups]
scope = "per_user"
```

Provider-specific pruning knobs remain provider config, not session-store config.

## Tests

### v0 store

- **TestCreateSession**: Load nonexistent DM session → new session created with defaults.
- **TestAdmissionRequired**: Unapproved DM cannot create or resume a session.
- **TestSaveAndLoad**: Save messages → load → messages match.
- **TestAppendOnly**: Save 3 messages, then save 2 more → load returns all 5 in order.
- **TestTurnIndex**: Messages have monotonically increasing `turn_index`.
- **TestExpireIdle**: Idle session is deleted.
- **TestExpireKeepsActive**: Active session survives expiry sweep.
- **TestCascadeDelete**: Session deletion cascades to messages and compaction log tables.
- **TestConcurrentReads**: Concurrent reads succeed under WAL mode.
- **TestWALMode**: `PRAGMA journal_mode` returns `wal`.

### v0 context assembly

- **TestAssembleBasic**: System prompt + persisted messages assemble in order.
- **TestCompactMessagesExcluded**: `compacted=1` messages are excluded from active history.
- **TestWorkspaceFilesReloadedEachTurn**: updated `MEMORY.md` / `HEARTBEAT.md` content appears on the next turn.

### v0.5 isolation and digests

- **TestAdminUsesRealWorkspace**: admin session binds to the real workspace root.
- **TestApprovedUserUsesIsolatedWorkspace**: non-admin session binds to its isolated workspace root.
- **TestApprovedUserReadOnlySharedPersona**: non-admin session can read but not write shared persona and shared memory.
- **TestDigestEnqueuedForAdmin**: non-admin session produces a bounded summary stored as `review_event`.
- **TestDigestDeliveredToAdminDM**: pending `review_events` are forwarded into the admin DM.
- **TestDigestIsBounded**: long non-admin session becomes a bounded digest under configured limits.

### Deferred compaction and pruning

- **TestPruningSoft**
- **TestPruningHard**
- **TestPruningPreservesNonTool**
- **TestPruningInMemoryOnly**
- **TestCompactionTrigger**
- **TestCompactionSummarize**
- **TestCompactionTruncate**
- **TestCompactionLog**
- **TestCompactionCacheStateReset**

### Deferred group sessions

- **TestPerUserGroupSession**
- **TestSharedGroupSession**
- **TestGroupSessionKey**

### Schema

- **TestSchemaVersion**
- **TestRoleConstraint**
- **TestContentChars**
- **TestCompactedIndex**

### Integration

- **TestFullSessionLifecycle**: DM session can continue across many turns, survive restarts, and expire cleanly.
- **TestAdminReviewFlow**: non-admin work stays isolated but periodic digests appear in the admin DM without requiring a separate UI.
