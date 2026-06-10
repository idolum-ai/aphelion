# Sessions — Conversation State, Admission, Isolation & Review Flow

_Current status:_ the current scope is admitted Telegram DMs, configured
durable-child/group sessions, SQLite session state, TES retention, review
digests, and isolation-aware prompt assembly.

## Overview

A session is the durable conversation ledger for one agent conversation. It stores message history, token/accounting metadata, compaction markers, and enough information to continue the conversation on the next turn.

This spec separates three concerns that should stay distinct:

- **principal policy**: who is allowed to talk to the system, and at what authority level
- **session ledger**: the durable append-only transcript for a conversation
- **review flow**: the bounded summaries that move information from isolated non-admin sessions into the admin DM

This separation matters because authority is not the same thing as conversation history. The session itself should remain a clean conversation ledger.

In the governor/face architecture, the session ledger primarily stores the user-visible conversation. Governor-internal decisions may later be recorded as sidecar audit metadata, but they should not silently replace the visible transcript.

Operational work follows the same split:

- visible transcript = what the user actually saw
- operation/proposal state = machine-authored durable sidecar for ongoing work

The key rule is:

- delivered `Idolum` scene = visible conversation record
- governor floor sidecar = audit record

Interrupted execution follows the same pattern:

- TES execution events = canonical machine-authored source of truth
- structured turn-run facts = operational startup recovery hints, not status truth
- governor recovery analysis = maintenance interpretation layered on top

## Truth-Class Contract (Normative)

Session-adjacent surfaces must be classified using exactly these current classes:

- `canonical`
- `projection`
- `operational current-state store`

Session requirements align to that contract as follows:

- canonical:
  - `session.execution_events` for execution-sequence truth
  - `session.messages` for scene transcript truth
  - `messages.floor_content` and `messages.floor_metadata` for recorded floor
    payloads
  - `session.outbound_messages` for transport-ledger recorded deliveries
  - `session.review_events` rows with `status='delivered'` for delivered review
    transcript history
- operational current-state store:
  - plan and operation sidecars
  - pending decision and continuation state
  - latest floor snapshots (`sessions.last_floor_text`,
    `sessions.last_floor_metadata`)
  - `session.review_events` rows with `status='pending'` for governance queue
    state
  - `turn_runs` for startup recovery parking and in-process run bookkeeping
- projection:
  - `/status`, `/health trace`, and quick-read rendering surfaces

TES retention invariants:

- when `sessions.tes_retention.enabled = true`, prune candidates must be
  exported to `sessions.tes_retention.export_dir` before deletion;
- TES prune must fail closed if export writing fails;
- TES prune must preserve at least `min_retained_rows` newest rows and delete at
  most `max_delete_per_gc` rows per run.

## Scope

### Current required

- admitted Telegram DM sessions and configured durable-child/group sessions
- explicit principal resolution before a chat can create or resume a session
- At least one admin principal
- SQLite-backed append-only message history
- Per-turn load, run, and save
- Stable Telegram DM session identity
- Session expiry support
- Prompt assembly from workspace files plus persisted active history

### Approved multi-user DMs

- `admin` and `approved_user` principal roles
- Hard isolation for non-admin writable state
- Read-only access to global persona and shared memory for non-admins
- Automatic bounded digests forwarded from non-admin sessions into the admin DM
- The admin DM acts as the review UI; no separate dashboard is required

### Research or deferred

- In-memory pruning policies
- Automatic compaction triggers and summary generation
- Cache-aware prompt fingerprinting and exact-byte prompt reuse
- Provider-specific cache heuristics coupled to session state

## Principals, Admission & Authority

### Principal model

For Telegram DM ingress, the principal is the Telegram user resolved by
Telegram `from.id`.

Principal policy is config-owned and defined in `principals.md`.

Unknown users are denied at ingress. There is no pending admission workflow.

### Authority roles

- `admin`: trusted to mutate global state
- `approved_user`: trusted to talk to the system, but not to mutate global state directly

### Bootstrap

The simplest correct current implementation bootstraps principal policy from config:

- one configured admin principal
- optional configured `approved_user` principals

This is sufficient for the first runnable system.

## Session Identity

Sessions are keyed by a composite of `chat_id + user_id`.

### DMs

- one session per Telegram DM
- key: `chat_id`
- persist `user_id = 0`
- persist `chat_type = "dm"`

The composite key remains because it is the right long-term shape for later group support.

### Deferred: Groups

Later group behavior should support:

- `"shared"`: one session per group, key `chat_id:0`
- `"per_user"`: one session per user per group, key `chat_id:user_id`

## Authority & Isolation

### Current Phase

If only the admin is approved, the session may operate directly on the real workspace and shared memory.

### Next Phase

Once more than one approved user exists, authority must split from admission.

- `admin` can write to the global workspace, global memory, and persona files
- `approved_user` can only write to isolated per-user state
- `approved_user` can read global persona and shared memory, but only as read-only prompt context
- `approved_user` cannot directly mutate shared memory, persona files, or the real workspace

The key rule is:

- **global mutation authority** belongs only to admin principals
- **local work authority** belongs to each approved non-admin principal inside isolated state
- **cross-session knowledge transfer** happens only through bounded digests into the admin DM

### Isolation roots

The design target is to stop treating one workspace path as both global identity and per-user writable state.

Instead, resolve four roots:

- `global_root`: shared persona/bootstrap files, admin-writable
- `shared_memory_root`: shared memory, admin-writable, non-admin read-only
- `user_workspace_root/<principal>`: writable isolated workspace for a non-admin principal
- `user_memory_root/<principal>`: writable isolated memory for a non-admin principal

For admin sessions:

- exec tools run in the real/global workspace
- shared memory is writable

For non-admin sessions:

- exec tools run only inside the isolated per-user workspace
- per-user memory is writable
- global persona and shared memory are read-only prompt context

### Process isolation

Storage isolation is not enough on its own. Tool execution must also run under a role-aware sandbox.

For `approved_user` sessions, the target model is:

- the process starts in an isolated execution root
- `/` is read-only, hidden, or replaced by a minimal root
- writable paths are limited to:
  - that principal's isolated workspace
  - that principal's isolated memory
  - `/tmp`
- global persona and shared memory are mounted read-only if exposed at all
- config, SSH keys, GPG material, and similar secrets are hidden
- Linux capabilities are dropped
- a user namespace is enabled
- cgroup limits are enforced
- network access is either disabled or explicitly allow-listed

For `admin` sessions, the process may use a more permissive profile, but it should still retain bounded time/resource controls. The key distinction is that admin execution may target the real/global workspace, while non-admin execution must never do so.

## Data Model

### Principal

```go
type Principal struct {
    TelegramUserID int64
    Role           string // "admin" | "approved_user"
    DisplayName    string
}
```

### Session

```go
type Session struct {
    ChatID       int64
    UserID       int64           // 0 in current implementation
    Messages     []Message
    SystemPrompt string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    TurnCount    int

    // Snapshot of the resolved execution root for audit/debugging.
    ResolvedWorkspaceRoot string

    // Sidecar audit artifact for the most recent governor floor text.
    // The visible ledger still stores the delivered scene reply.
    LastFloorText string

    // Cache tracking
    CacheState CacheState

    // Compaction
    CompactionLog []CompactionEntry

    // Planning / operations
    PlanState      PlanState
    OperationState OperationState

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
}
```

### OperationState

Operational work should be session-native durable state rather than an ad hoc tool-local object.

```go
type OperationState struct {
    ID        string
    Objective string
    Status    string
    Stage     string
    Summary   string
    Proposal  OperationProposal
    Findings  []OperationFinding
    Artifacts []OperationArtifact
    UpdatedAt time.Time
}
```

The operation state is sidecar machine state. It should inform prompts, approvals, and recovery without replacing the visible conversation history.

### ReviewEvent

```go
type ReviewEvent struct {
    ID               int64
    SourceChatID     int64
    SourceUserID     int64
    SourceRole       string
    TargetAdminChatID int64
    TurnFrom         int
    TurnTo           int
    Summary          string
    Status           string    // "pending" | "delivered" | "dismissed"
    CreatedAt        time.Time
    DeliveredAt      time.Time
}
```

### TurnRun

Structured turn-run records are sidecar execution artifacts, not visible conversation messages.

```go
type TurnRun struct {
    ID                int64
    ChatID            int64
    UserID            int64
    Kind              string    // "interactive" | "heartbeat" | "cron" | "recovery"
    Status            string    // "running" | "completed" | "failed" | "interrupted"
    RequestText       string
    StartedAt         time.Time
    CompletedAt       time.Time
    LastActivityAt    time.Time
    LastToolName      string
    LastToolPreview   string
    ToolCallsStarted  int
    ProgressMessageID int64
    ErrorText         string
    RecoverySummary   string
    RecoveryLoggedAt  time.Time
}
```

These records are restart/recovery execution hints and answer questions the
visible transcript cannot while a turn is in flight:

- what was in flight when the host restarted
- whether real tool execution had started
- which progress UI artifacts existed
- what the governor later concluded about recovery

### CacheState and CompactionEntry

```go
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
INSERT INTO schema_version (version) VALUES (<current schema version>);

-- current implementation principal policy is config-owned, not persisted in SQLite.

CREATE TABLE sessions (
    chat_id       INTEGER NOT NULL,
    user_id       INTEGER NOT NULL DEFAULT 0,
    system_prompt TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    turn_count    INTEGER NOT NULL DEFAULT 0,
    chat_type     TEXT NOT NULL DEFAULT 'dm',
    chat_title    TEXT,
    user_name     TEXT,
    resolved_workspace_root TEXT,
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
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    source_chat_id   INTEGER NOT NULL,
    source_user_id   INTEGER NOT NULL DEFAULT 0,
    source_role      TEXT NOT NULL,
    target_chat_id   INTEGER NOT NULL, -- admin DM chat_id
    turn_from        INTEGER,
    turn_to          INTEGER,
    summary          TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'delivered', 'dismissed')),
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    delivered_at     TEXT
);

CREATE INDEX idx_review_events_target ON review_events(target_chat_id, status, created_at);

CREATE TABLE turn_runs (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id                  TEXT NOT NULL,
    chat_id                     INTEGER NOT NULL DEFAULT 0,
    user_id                     INTEGER NOT NULL DEFAULT 0,
    scope_kind                  TEXT NOT NULL DEFAULT '',
    scope_id                    TEXT NOT NULL DEFAULT '',
    durable_agent_id            TEXT NOT NULL DEFAULT '',
    kind                        TEXT NOT NULL,
    turn_index                  INTEGER NOT NULL DEFAULT 0,
    status                      TEXT NOT NULL,
    request_text                TEXT NOT NULL,
    started_at                  TEXT NOT NULL,
    completed_at                TEXT,
    last_activity_at            TEXT NOT NULL,
    last_tool_name              TEXT,
    last_tool_preview           TEXT,
    tool_calls_started          INTEGER NOT NULL DEFAULT 0,
    tool_calls_finished         INTEGER NOT NULL DEFAULT 0,
    total_tool_chars_in         INTEGER NOT NULL DEFAULT 0,
    total_assistant_chars_out   INTEGER NOT NULL DEFAULT 0,
    provider_input_tokens       INTEGER NOT NULL DEFAULT 0,
    provider_output_tokens      INTEGER NOT NULL DEFAULT 0,
    provider_cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    provider_cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    last_tool_result_preview    TEXT,
    last_tool_error             TEXT,
    progress_message_id         INTEGER,
    error_text                  TEXT,
    recovery_summary            TEXT,
    recovery_logged_at          TEXT
);

CREATE INDEX idx_turn_runs_status ON turn_runs(status, started_at);

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

- **Principal policy is separate from sessions**: principal resolution happens before session load and is defined outside the session ledger.
- **Composite session key** `(chat_id, user_id)`: current implementation only uses `user_id=0`, but the shape is already correct for later group support.
- **Messages in a separate table**: supports efficient load, append, and filtering without rewriting a giant blob.
- **resolved_workspace_root**: records the actual execution root used for that session.
- **outbound_messages**: keeps a durable mapping between agent turns and Telegram message IDs.
- **review_events**: creates a one-way, bounded bridge from isolated sessions into the admin DM.
- **turn_runs**: preserves machine-authored facts about in-flight work,
  progress artifacts, provider/token accounting, and recovery state across
  restarts.
- **compacted flag**: compacted messages remain on disk for audit but can be excluded from active prompt assembly.

## Session Lifecycle

### Resolve principal

Before routing a DM:

1. resolve the principal from config/bootstrap or the `principals` table
2. if no principal is configured for that Telegram user, deny and do not create a session
3. if a principal is configured, continue into session load

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

### Save

```go
func (s *Store) Save(session *Session, newMessages []Message, usage TokenUsage) error {
    // In one transaction:
    // 1. INSERT new messages
    // 2. UPDATE sessions SET updated_at, turn_count, token totals, metadata
    // 3. COMMIT
}
```

The persistent history is append-only. Existing rows are not rewritten during the normal turn path.

### Turn-run tracking

Interactive, heartbeat, cron, and recovery turns should also create a structured `turn_run` sidecar row.

Normal lifecycle:

1. insert `turn_runs.status = "running"` before the governor turn begins
2. update `last_tool_name`, `last_tool_preview`, tool counters, provider
   token/cache counters, assistant/tool character counts, and
   `progress_message_id` as work happens
3. mark the row `completed` or `failed` when the turn ends normally

If the process disappears before step 3, the next startup should mark the row `interrupted` and feed it into maintenance recovery analysis.
These accounting fields support status and doctor projections. They are
operational current-state hints, not canonical execution history; TES remains
the canonical record of event order and runtime evidence.

### Delete / Expire

```go
func (s *Store) ExpireIdle(maxIdle time.Duration) (int, error) {
    // DELETE idle sessions
    // CASCADE deletes messages, outbound_messages, and compaction_log
}
```

Expiry is useful in current implementation even before compaction exists.

## Review Flow

### Isolated digest membrane

The admin DM is the review surface. No separate UI is required.

Non-admin sessions stay isolated, but they periodically emit bounded digests into the admin DM:

1. summarize a bounded slice of the non-admin session
2. store it as a `review_event`
3. forward it to the admin DM on a cadence or when the session goes idle
4. append the delivered digest to the admin DM as a labeled bot-generated message
5. let the admin react naturally in the same DM

The digest itself is the membrane:

- raw session history does not cross the boundary
- raw tool output does not cross the boundary
- global state is not mutated by the non-admin session directly
- the admin can still ban the user, delete the session, or dismiss the digest

The review flow is intentionally one-way:

- non-admin session -> bounded digest -> admin DM

The reverse direction is ordinary admin action in the admin DM, not silent state sharing back into non-admin sessions.

## Disruption Recovery

Service restarts, crashes, deploys, and operator interruptions can cut a turn off mid-execution.

Aphelion should handle that in two phases:

1. **machine phase**
   - detect `turn_runs.status = "running"` on startup
   - mark them `interrupted`
   - preserve the raw structured facts
2. **governor phase**
   - run a maintenance analysis over those facts
   - append the analysis to the maintenance ledger
   - optionally surface a summarized recovery note later

The governor should analyze interruptions, but it should not be the only witness of them.

### Recovery note location

The default place for disruption analysis is the maintenance ledger, not the interrupted user DM.

That keeps:

- user transcripts clean
- recovery inspectable
- startup recovery aligned with heartbeat and other maintenance work

### Recovery content

Recovery analysis should focus on:

- what was interrupted
- whether any tool work had started
- which progress artifacts were visible
- what likely needs retry, resume, or manual inspection

No explicit promotion workflow is required. The digest is already a reduced, bounded transfer of context.

## Context Assembly

### Prompt assembly

Every turn:

1. render the base system instruction
2. load workspace bootstrap files
3. load workspace dynamic files (`MEMORY.md`, `HEARTBEAT.md`, daily notes)
4. load persisted messages for the session
5. exclude compacted messages from active history
6. append the new user message
7. run the model turn
8. persist new messages

current implementation does **not** require:

- multi-user isolation
- digest forwarding
- pruning tool outputs
- automatic compaction triggers
- prompt fingerprinting
- exact-byte prompt reuse
- provider cache breakpoints in the session layer

The only current implementation requirement is correctness: workspace files must be reflected on the next turn, and active persisted history must be replayed in order.

Governor prompt and face prompt are separate logical artifacts, even if current implementation initially keeps the face thin.

### Isolated prompt assembly

For non-admin sessions:

1. use the isolated per-user workspace root for tool execution
2. inject global persona/bootstrap files as read-only context
3. inject shared memory as read-only context
4. inject per-user local memory as writable local context
5. never grant direct write access to global persona or shared memory

For the admin session:

1. use the real/global workspace root
2. receive forwarded digests from other sessions as labeled bot messages
3. treat those digests as normal conversational context inside the admin DM

### Deferred: Cache-aware prompt reuse

Later, prompt assembly should distinguish:

- **stable prefix**: bootstrap files and other rarely changing instructions
- **dynamic suffix**: `MEMORY.md`, `HEARTBEAT.md`, daily notes, runtime metadata

When this lands, unchanged stable content should be reused byte-for-byte to preserve provider cache prefixes.

## Compaction

Compaction is deferred after current implementation, but becomes more useful in future phase because digests are the mechanism for carrying bounded information from isolated non-admin sessions into the admin DM.

The design target is:

1. detect when assembled context exceeds `max_context_ratio * context_window`
2. summarize or truncate older turns
3. mark replaced messages as `compacted = 1`
4. insert a summary message at the compaction boundary
5. record the event in `compaction_log`

For non-admin sessions, the same summarization machinery can also produce `review_events` for the admin DM.

Compacted messages should remain on disk for audit. They should not be deleted as part of normal compaction.

## Pruning

Pruning is deferred after current implementation.

When implemented, pruning is applied only in memory during prompt assembly:

- older tool results may be soft-trimmed
- older tool results may later be hard-cleared
- user and assistant conversational messages are never pruned

SQLite remains the source of truth for the full original transcript.

## Store Interfaces

### Session ledger

```go
type SessionKey struct {
    ChatID int64
    UserID int64 // always 0 in current implementation
}

type Store interface {
    Load(key SessionKey) (*Session, error)
    Save(session *Session, newMessages []Message, usage TokenUsage) error
    ExpireIdle(maxIdle time.Duration) (int, error)
    Close() error
}
```

For current implementation, `Save(...)` should preserve the delivered assistant scene in the visible transcript. The governor floor should be stored alongside the session as audit data rather than appended as a second visible assistant message.

### Principal policy

current implementation may keep principal policy in config. If it becomes durable earlier, expose a separate principal-policy interface rather than overloading the session ledger.

```go
type PrincipalPolicy interface {
    Resolve(chatID int64) (*Principal, error)
    Approve(chatID int64, role string) error
    Ban(chatID int64) error
}
```

### Extended interfaces after current implementation

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

Single-connection SQLite with WAL mode is sufficient for current implementation. A dedicated writer goroutine is an acceptable later refinement if write contention shows up, but it is not required for the first usable system.

## Config (see `config.md`)

### Required

```toml
[sessions]
db_path = "~/.aphelion/state/sessions.db"
idle_expiry = "24h"

[principals.telegram]
admin_user_ids = [123456789]
# at least one admin user is required
```

### Next Phase

```toml
[reviews]
enabled = true
digest_every = "30m"
digest_on_idle = true
max_summary_chars = 1200

[sessions.isolation]
global_root = "~/.aphelion/agent"
shared_memory_root = "~/.aphelion/agent"
user_workspace_root = "~/.aphelion/state/isolated/workspaces"
user_memory_root = "~/.aphelion/state/isolated/memory"
```

### Deferred

```toml
[sessions]
max_context_ratio = 0.75
compaction_ratio = 0.55
compaction_strategy = "summarize"
compaction_model = ""

[sessions.groups]
scope = "per_user"
```

Provider-specific pruning knobs remain provider config, not session-ledger config.

## Tests

### Admission and ledger

- **TestAdmissionRequired**: unknown DM cannot create or resume a session
- **TestBootstrapAdminConfigured**: configured admin principal is available on startup
- **TestConfiguredApprovedUserAllowed**: configured approved user can create or resume a DM session
- **TestCreateSession**: load nonexistent approved DM session → new session created with defaults
- **TestSaveAndLoad**: save messages → load → messages match
- **TestAppendOnly**: save 3 messages, then save 2 more → load returns all 5 in order
- **TestTurnIndex**: messages have monotonically increasing `turn_index`
- **TestExpireIdle**: idle session is deleted
- **TestExpireKeepsActive**: active session survives expiry sweep
- **TestConcurrentReads**: concurrent reads succeed under WAL mode
- **TestWALMode**: `PRAGMA journal_mode` returns `wal`
- **TestVisibleLedgerStoresDeliveredScene**: visible assistant history stores the delivered Idolum scene
- **TestFloorStoredAsSidecarAudit**: governor floor text is stored alongside the session without polluting the visible transcript

### Context assembly

- **TestAssembleBasic**: system prompt + persisted messages assemble in order
- **TestCompactMessagesExcluded**: `compacted=1` messages are excluded from active history
- **TestWorkspaceFilesReloadedEachTurn**: updated `MEMORY.md` / `HEARTBEAT.md` content appears on the next turn

### Isolation and digests

- **TestAdminUsesGlobalRoots**: admin session binds to the real/global roots
- **TestApprovedUserUsesIsolatedRoots**: non-admin session binds to isolated workspace and memory roots
- **TestApprovedUserReadOnlySharedMemory**: non-admin can read but not write shared memory/persona surfaces
- **TestApprovedUserExecUsesIsolatedRoot**: non-admin tool execution starts inside the isolated execution root
- **TestApprovedUserCannotWriteGlobalRoot**: non-admin exec cannot modify the global workspace
- **TestApprovedUserCannotWriteSharedMemory**: non-admin exec cannot modify shared memory or persona files
- **TestApprovedUserCannotReadHiddenSecrets**: non-admin exec cannot read config, SSH, or GPG paths exposed on the host
- **TestApprovedUserSandboxHasDroppedCaps**: non-admin exec lacks elevated Linux capabilities
- **TestApprovedUserSandboxHasNamespaceIsolation**: non-admin exec runs inside the expected namespace profile
- **TestApprovedUserSandboxNetworkPolicy**: non-admin exec is denied or restricted according to sandbox policy
- **TestDigestEnqueuedForAdmin**: non-admin session produces a bounded `review_event`
- **TestDigestDeliveredToAdminDM**: pending `review_events` are forwarded into the admin DM
- **TestDigestAppendedToAdminSession**: delivered digest becomes a labeled bot-generated message in the admin DM history
- **TestDigestIsBounded**: long non-admin session becomes a bounded digest under configured limits

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
