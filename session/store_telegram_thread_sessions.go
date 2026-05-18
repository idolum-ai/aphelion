//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"time"
)

func telegramThreadSessionKey(chatID int64, threadID int64) SessionKey {
	return SessionKey{ChatID: chatID, UserID: 0, Scope: TelegramThreadScopeRef(chatID, threadID)}
}

func ensureTelegramThreadSessionTx(tx *sql.Tx, chatID int64, threadID int64, now time.Time) error {
	if chatID == 0 || threadID <= 0 {
		return nil
	}
	if err := ensureSessionRowTx(tx, telegramThreadSessionKey(chatID, threadID), "dm", now); err != nil {
		return fmt.Errorf("ensure telegram thread session: %w", err)
	}
	return nil
}

func ensureTelegramThreadSessions(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("ensure telegram thread sessions: transaction is required")
	}
	hasThreads, err := schemaTableExists(tx, "telegram_threads")
	if err != nil {
		return err
	}
	if !hasThreads {
		return nil
	}
	hasSessions, err := schemaTableExists(tx, "sessions")
	if err != nil {
		return err
	}
	if !hasSessions {
		return nil
	}
	rows, err := tx.Query(`
		SELECT chat_id, thread_id, COALESCE(NULLIF(created_at, ''), datetime('now'))
		FROM telegram_threads
		WHERE chat_id != 0 AND thread_id > 0
		ORDER BY chat_id, thread_id
	`)
	if err != nil {
		return fmt.Errorf("query telegram threads for session backfill: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			chatID    int64
			threadID  int64
			createdAt string
		)
		if err := rows.Scan(&chatID, &threadID, &createdAt); err != nil {
			return fmt.Errorf("scan telegram thread session backfill: %w", err)
		}
		at, err := parseSQLiteTime(createdAt)
		if err != nil {
			at = time.Now().UTC()
		}
		if err := ensureTelegramThreadSessionTx(tx, chatID, threadID, at); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate telegram thread session backfill: %w", err)
	}
	return nil
}
