//go:build linux

package session

import "time"

type TelegramIngressFailureRecord struct {
	ID         int64
	Surface    string
	UpdateID   int64
	UpdateKind string
	ChatID     int64
	SenderID   int64
	MessageID  int64
	ErrorText  string
	Payload    string
	CreatedAt  time.Time
}

type TelegramIngressUpdateStatus string

const (
	TelegramIngressUpdateAccepted    TelegramIngressUpdateStatus = "accepted"
	TelegramIngressUpdateQueued      TelegramIngressUpdateStatus = "queued"
	TelegramIngressUpdateRunning     TelegramIngressUpdateStatus = "running"
	TelegramIngressUpdateCompleted   TelegramIngressUpdateStatus = "completed"
	TelegramIngressUpdateFailed      TelegramIngressUpdateStatus = "failed"
	TelegramIngressUpdateDropped     TelegramIngressUpdateStatus = "dropped"
	TelegramIngressUpdateInterrupted TelegramIngressUpdateStatus = "interrupted"
	TelegramIngressUpdateSkipped     TelegramIngressUpdateStatus = "skipped"
)

const (
	TelegramIngressDropReasonTelegramThreadClosed  = "telegram_thread_closed"
	TelegramIngressDropReasonTelegramThreadMissing = "telegram_thread_missing"
)

type TelegramIngressUpdateRecord struct {
	Surface     string
	UpdateID    int64
	UpdateKind  string
	ChatID      int64
	SenderID    int64
	MessageID   int64
	SessionID   string
	Status      TelegramIngressUpdateStatus
	TurnRunID   int64
	ErrorText   string
	InboundJSON string
	PayloadJSON string
	AcceptedAt  time.Time
	QueuedAt    time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	UpdatedAt   time.Time
}

type TelegramIngressTransitionResult struct {
	Record   TelegramIngressUpdateRecord
	Found    bool
	Dispatch bool
	Queued   bool
	Terminal bool
}

func TelegramIngressUpdateStatusTerminal(status TelegramIngressUpdateStatus) bool {
	switch normalizeTelegramIngressUpdateStatus(status) {
	case TelegramIngressUpdateCompleted, TelegramIngressUpdateFailed, TelegramIngressUpdateDropped, TelegramIngressUpdateInterrupted, TelegramIngressUpdateSkipped:
		return true
	default:
		return false
	}
}

func TelegramIngressUpdateStatusDispatchable(status TelegramIngressUpdateStatus) bool {
	switch normalizeTelegramIngressUpdateStatus(status) {
	case TelegramIngressUpdateAccepted, TelegramIngressUpdateQueued:
		return true
	default:
		return false
	}
}

func telegramIngressTransitionResult(record TelegramIngressUpdateRecord, found bool) TelegramIngressTransitionResult {
	if !found {
		return TelegramIngressTransitionResult{}
	}
	return TelegramIngressTransitionResult{
		Record:   record,
		Found:    true,
		Dispatch: TelegramIngressUpdateStatusDispatchable(record.Status),
		Queued:   record.Status == TelegramIngressUpdateQueued,
		Terminal: TelegramIngressUpdateStatusTerminal(record.Status),
	}
}

func telegramIngressSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS telegram_ingress_offsets (
			surface TEXT PRIMARY KEY,
			next_update_id INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS telegram_ingress_failures (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			surface TEXT NOT NULL DEFAULT '',
			update_id INTEGER NOT NULL DEFAULT 0,
			update_kind TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			message_id INTEGER NOT NULL DEFAULT 0,
			error_text TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_ingress_failures_surface_created ON telegram_ingress_failures(surface, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_ingress_failures_update ON telegram_ingress_failures(surface, update_id)`,
		`CREATE TABLE IF NOT EXISTS telegram_ingress_updates (
			surface TEXT NOT NULL,
			update_id INTEGER NOT NULL,
			update_kind TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			message_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'accepted',
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			error_text TEXT NOT NULL DEFAULT '',
			inbound_json TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			accepted_at TEXT NOT NULL DEFAULT (datetime('now')),
			queued_at TEXT,
			started_at TEXT,
			completed_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(surface, update_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_ingress_updates_status ON telegram_ingress_updates(surface, status, update_id)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_ingress_updates_session ON telegram_ingress_updates(session_id, updated_at DESC)`,
	}
}
