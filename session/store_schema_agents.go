//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureTelegramAgentMessageTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_agent_messages (
			chat_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			surface TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, message_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_agent_messages_agent ON telegram_agent_messages(agent_id, updated_at DESC) WHERE agent_id != ''`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram agent message table: %w", err)
		}
	}
	return nil
}
