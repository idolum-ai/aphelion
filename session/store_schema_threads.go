//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureTelegramReplyRoutingIndexes(tx *sql.Tx) error {
	for _, migration := range []struct {
		table string
		stmt  string
	}{
		{table: "outbound_messages", stmt: `CREATE INDEX IF NOT EXISTS idx_outbound_chat_message ON outbound_messages(chat_id, telegram_msg_id)`},
		{table: "telegram_ingress_updates", stmt: `CREATE INDEX IF NOT EXISTS idx_telegram_ingress_updates_message ON telegram_ingress_updates(chat_id, message_id)`},
		{table: "telegram_threads", stmt: `CREATE INDEX IF NOT EXISTS idx_telegram_threads_created_message ON telegram_threads(chat_id, created_message_id) WHERE created_message_id > 0`},
		{table: "pending_decisions", stmt: `CREATE INDEX IF NOT EXISTS idx_pending_decisions_delivery_message ON pending_decisions(chat_id, delivery_message_id) WHERE delivery_message_id > 0`},
	} {
		exists, err := schemaTableExists(tx, migration.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(migration.stmt); err != nil {
			return fmt.Errorf("ensure telegram reply routing index: %w", err)
		}
	}
	return nil
}

func ensureTurnProgressViewTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS turn_progress_views (
			run_id INTEGER PRIMARY KEY,
			message_id INTEGER NOT NULL DEFAULT 0,
			selected_view TEXT NOT NULL DEFAULT 'summary' CHECK(selected_view IN ('summary', 'details')),
			summary_text TEXT NOT NULL DEFAULT '',
			details_text TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (run_id) REFERENCES turn_runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_progress_views_message ON turn_progress_views(message_id, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure turn progress view table: %w", err)
		}
	}
	return nil
}

func ensureTelegramThreadTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_threads (
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open', 'closed')),
			created_by_sender_id INTEGER NOT NULL DEFAULT 0,
			created_from_update_id INTEGER NOT NULL DEFAULT 0,
			created_message_id INTEGER NOT NULL DEFAULT 0,
			created_text TEXT NOT NULL DEFAULT '',
			display_slot INTEGER NOT NULL DEFAULT 0,
			archived_display_name TEXT NOT NULL DEFAULT '',
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			closed_at TEXT,
			absorb_summary TEXT NOT NULL DEFAULT '',
			absorbed_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, thread_id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_telegram_threads_created_update ON telegram_threads(chat_id, created_from_update_id) WHERE created_from_update_id > 0`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_threads_created_message ON telegram_threads(chat_id, created_message_id) WHERE created_message_id > 0`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_threads_chat_status ON telegram_threads(chat_id, status, updated_at DESC, thread_id DESC)`,
		`CREATE TABLE IF NOT EXISTS telegram_thread_last_messages (
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, thread_id),
			FOREIGN KEY(chat_id, thread_id) REFERENCES telegram_threads(chat_id, thread_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_thread_last_messages_message ON telegram_thread_last_messages(chat_id, message_id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram thread table: %w", err)
		}
	}
	if err := ensureTelegramThreadDisplayColumns(tx); err != nil {
		return err
	}
	return nil
}

func ensureTelegramThreadDisplayColumns(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "telegram_threads")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, column := range []schemaColumnMigration{
		{table: "telegram_threads", column: "display_slot", statement: `ALTER TABLE telegram_threads ADD COLUMN display_slot INTEGER NOT NULL DEFAULT 0`},
		{table: "telegram_threads", column: "archived_display_name", statement: `ALTER TABLE telegram_threads ADD COLUMN archived_display_name TEXT NOT NULL DEFAULT ''`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		`UPDATE telegram_threads
		SET display_slot = (
			SELECT COUNT(*)
			FROM telegram_threads AS open_threads
			WHERE open_threads.chat_id = telegram_threads.chat_id
				AND open_threads.status = 'open'
				AND open_threads.thread_id <= telegram_threads.thread_id
		)
		WHERE status = 'open' AND display_slot <= 0`,
		`UPDATE telegram_threads
		SET display_slot = 0,
			archived_display_name = CASE
				WHEN TRIM(archived_display_name) != '' THEN archived_display_name
				ELSE thread_id || '-' || SUBSTR(COALESCE(NULLIF(absorbed_at, ''), NULLIF(closed_at, ''), NULLIF(updated_at, ''), NULLIF(created_at, ''), datetime('now')), 1, 10)
			END
		WHERE status != 'open'`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("backfill telegram thread display columns: %w", err)
		}
	}
	return nil
}

func ensureTelegramThreadPromotionHandoffTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_thread_promotion_handoffs (
			handoff_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			display_slot INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft', 'ready', 'approved', 'cancelled', 'superseded')),
			created_by_sender_id INTEGER NOT NULL DEFAULT 0,
			source_session_id TEXT NOT NULL DEFAULT '',
			source_thread_status TEXT NOT NULL DEFAULT '',
			source_preview TEXT NOT NULL DEFAULT '',
			context_summary TEXT NOT NULL DEFAULT '',
			memory_digest_json TEXT NOT NULL DEFAULT '[]',
			resource_review_json TEXT NOT NULL DEFAULT '[]',
			policy_patch_json TEXT NOT NULL DEFAULT '{}',
			proposed_child_json TEXT NOT NULL DEFAULT '{}',
			first_task TEXT NOT NULL DEFAULT '',
			validation_json TEXT NOT NULL DEFAULT '[]',
			review_checklist_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(chat_id, thread_id) REFERENCES telegram_threads(chat_id, thread_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_thread_promotion_handoffs_thread ON telegram_thread_promotion_handoffs(chat_id, thread_id, updated_at DESC, handoff_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_thread_promotion_handoffs_status ON telegram_thread_promotion_handoffs(status, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_telegram_thread_promotion_handoffs_open_draft ON telegram_thread_promotion_handoffs(chat_id, thread_id) WHERE status = 'draft'`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram thread promotion handoff table: %w", err)
		}
	}
	return ensureTelegramThreadPromotionReviewColumns(tx)
}

func ensureTelegramThreadPromotionReviewColumns(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "telegram_thread_promotion_handoffs")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, column := range []schemaColumnMigration{
		{table: "telegram_thread_promotion_handoffs", column: "proposed_child_json", statement: `ALTER TABLE telegram_thread_promotion_handoffs ADD COLUMN proposed_child_json TEXT NOT NULL DEFAULT '{}'`},
		{table: "telegram_thread_promotion_handoffs", column: "first_task", statement: `ALTER TABLE telegram_thread_promotion_handoffs ADD COLUMN first_task TEXT NOT NULL DEFAULT ''`},
		{table: "telegram_thread_promotion_handoffs", column: "validation_json", statement: `ALTER TABLE telegram_thread_promotion_handoffs ADD COLUMN validation_json TEXT NOT NULL DEFAULT '[]'`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return fmt.Errorf("ensure telegram thread promotion review column %s: %w", column.column, err)
		}
	}
	return nil
}

func migrateSchemaV56ToV57(tx *sql.Tx) error {
	if err := ensureTelegramThreadPromotionHandoffTables(tx); err != nil {
		return fmt.Errorf("migrate schema v56 to v57 ensure telegram thread promotion review columns: %w", err)
	}
	return nil
}

func ensureTelegramCallbackMessageTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_callback_messages (
			chat_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL DEFAULT 0,
			surface TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, message_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_callback_messages_thread ON telegram_callback_messages(chat_id, thread_id, updated_at DESC) WHERE thread_id > 0`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram callback message table: %w", err)
		}
	}
	return nil
}

func ensureTelegramMediaPickerTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_media_thread_pickers (
			chat_id INTEGER NOT NULL,
			picker_message_id INTEGER NOT NULL,
			source_message_id INTEGER NOT NULL DEFAULT 0,
			source_ingress_surface TEXT NOT NULL DEFAULT '',
			source_ingress_update_id INTEGER NOT NULL DEFAULT 0,
			inbound_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'routed', 'cleared')),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, picker_message_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_media_thread_pickers_status ON telegram_media_thread_pickers(chat_id, status, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram media thread picker table: %w", err)
		}
	}
	return nil
}

func ensureTelegramMediaPickerSourceIngressColumns(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "telegram_media_thread_pickers")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, column := range []schemaColumnMigration{
		{table: "telegram_media_thread_pickers", column: "source_ingress_surface", statement: `ALTER TABLE telegram_media_thread_pickers ADD COLUMN source_ingress_surface TEXT NOT NULL DEFAULT ''`},
		{table: "telegram_media_thread_pickers", column: "source_ingress_update_id", statement: `ALTER TABLE telegram_media_thread_pickers ADD COLUMN source_ingress_update_id INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	return nil
}

func ensureTelegramThreadReminderTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_thread_reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'ignored', 'absorbed', 'resumed', 'expired')),
			summary_text TEXT NOT NULL DEFAULT '',
			summary_kind TEXT NOT NULL DEFAULT '',
			source_last_activity_at TEXT,
			created_by_sender_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(chat_id, thread_id) REFERENCES telegram_threads(chat_id, thread_id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_telegram_thread_reminders_message ON telegram_thread_reminders(chat_id, message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_thread_reminders_thread_status ON telegram_thread_reminders(chat_id, thread_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_thread_reminders_status ON telegram_thread_reminders(chat_id, status, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram thread reminder table: %w", err)
		}
	}
	return nil
}
