//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type TelegramMediaThreadPicker struct {
	ChatID          int64
	PickerMessageID int64
	SourceMessageID int64
	Inbound         core.InboundMessage
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (s *SQLiteStore) RecordTelegramMediaThreadPicker(chatID int64, pickerMessageID int64, inbound core.InboundMessage, at time.Time) error {
	if s == nil || s.db == nil || chatID == 0 || pickerMessageID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	inbound = sanitizeTelegramMediaPickerInbound(inbound)
	raw, err := json.Marshal(inbound)
	if err != nil {
		return fmt.Errorf("marshal telegram media picker inbound: %w", err)
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	_, err = s.db.Exec(`INSERT INTO telegram_media_thread_pickers(chat_id, picker_message_id, source_message_id, inbound_json, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'pending', ?, ?)
		ON CONFLICT(chat_id, picker_message_id) DO UPDATE SET
			source_message_id = excluded.source_message_id,
			inbound_json = excluded.inbound_json,
			status = 'pending',
			updated_at = excluded.updated_at`, chatID, pickerMessageID, inbound.MessageID, string(raw), stamp, stamp)
	if err != nil {
		return fmt.Errorf("record telegram media thread picker: %w", err)
	}
	return nil
}

func sanitizeTelegramMediaPickerInbound(inbound core.InboundMessage) core.InboundMessage {
	inbound.Raw = nil
	for i := range inbound.Artifacts {
		inbound.Artifacts[i].Data = nil
	}
	return inbound
}

func (s *SQLiteStore) TelegramMediaThreadPicker(chatID int64, pickerMessageID int64) (TelegramMediaThreadPicker, bool, error) {
	if s == nil || s.db == nil || chatID == 0 || pickerMessageID <= 0 {
		return TelegramMediaThreadPicker{}, false, nil
	}
	row := s.db.QueryRow(`SELECT chat_id, picker_message_id, source_message_id, inbound_json, status, created_at, updated_at
		FROM telegram_media_thread_pickers WHERE chat_id = ? AND picker_message_id = ?`, chatID, pickerMessageID)
	var rec TelegramMediaThreadPicker
	var raw, created, updated string
	if err := row.Scan(&rec.ChatID, &rec.PickerMessageID, &rec.SourceMessageID, &raw, &rec.Status, &created, &updated); err != nil {
		if err == sql.ErrNoRows {
			return TelegramMediaThreadPicker{}, false, nil
		}
		return TelegramMediaThreadPicker{}, false, fmt.Errorf("get telegram media thread picker: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(rec.Status), "pending") {
		return TelegramMediaThreadPicker{}, false, nil
	}
	if err := json.Unmarshal([]byte(raw), &rec.Inbound); err != nil {
		return TelegramMediaThreadPicker{}, false, fmt.Errorf("decode telegram media thread picker inbound: %w", err)
	}
	rec.Inbound = sanitizeTelegramMediaPickerInbound(rec.Inbound)
	rec.CreatedAt, _ = parseSQLiteTime(created)
	rec.UpdatedAt, _ = parseSQLiteTime(updated)
	return rec, true, nil
}

func (s *SQLiteStore) MarkTelegramMediaThreadPickerStatus(chatID int64, pickerMessageID int64, status string, at time.Time) error {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "routed"
	}
	if s == nil || s.db == nil || chatID == 0 || pickerMessageID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := s.db.Exec(`UPDATE telegram_media_thread_pickers SET status = ?, updated_at = ? WHERE chat_id = ? AND picker_message_id = ?`, status, at.UTC().Format(time.RFC3339Nano), chatID, pickerMessageID); err != nil {
		return fmt.Errorf("mark telegram media thread picker %s: %w", status, err)
	}
	return nil
}
