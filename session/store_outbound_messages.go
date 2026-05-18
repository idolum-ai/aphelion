//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
)

func (s *SQLiteStore) RecordOutbound(key SessionKey, turnIndex int, telegramMsgID int64, msgType string) error {
	return recordOutboundExec(s.db, key, turnIndex, telegramMsgID, msgType)
}

func recordOutboundTx(tx *sql.Tx, key SessionKey, turnIndex int, telegramMsgID int64, msgType string) error {
	return recordOutboundExec(tx, key, turnIndex, telegramMsgID, msgType)
}

func recordOutboundExec(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, key SessionKey, turnIndex int, telegramMsgID int64, msgType string) error {
	if telegramMsgID == 0 {
		return fmt.Errorf("record outbound: telegram_msg_id is required")
	}
	if strings.TrimSpace(msgType) == "" {
		msgType = "text"
	}
	sessionID := SessionIDForKey(key)

	_, err := exec.Exec(`
		INSERT INTO outbound_messages(session_id, chat_id, user_id, turn_index, telegram_msg_id, msg_type)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sessionID, key.ChatID, key.UserID, turnIndex, telegramMsgID, msgType)
	if err != nil {
		return fmt.Errorf("record outbound: %w", err)
	}
	return nil
}
