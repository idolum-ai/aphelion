//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) RecordTelegramAgentMessage(chatID int64, messageID int64, agentID string, surface string, at time.Time) error {
	if chatID == 0 || messageID <= 0 || strings.TrimSpace(agentID) == "" {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	atRaw := at.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO telegram_agent_messages(chat_id, message_id, agent_id, surface, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			agent_id = excluded.agent_id,
			surface = excluded.surface,
			updated_at = excluded.updated_at
	`, chatID, messageID, strings.TrimSpace(agentID), clampStoreText(surface, 120), atRaw, atRaw); err != nil {
		return fmt.Errorf("record telegram agent message: %w", err)
	}
	return nil
}

func (s *SQLiteStore) TelegramAgentIDForReplyMessage(chatID int64, messageID int64) (string, bool, error) {
	if chatID == 0 || messageID <= 0 {
		return "", false, nil
	}
	var agentID string
	err := s.db.QueryRow(`
		SELECT agent_id
		FROM telegram_agent_messages
		WHERE chat_id = ? AND message_id = ? AND agent_id != ''
		LIMIT 1
	`, chatID, messageID).Scan(&agentID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lookup telegram agent reply message: %w", err)
	}
	agentID = strings.TrimSpace(agentID)
	return agentID, agentID != "", nil
}
