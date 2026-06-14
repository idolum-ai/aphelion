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

const (
	TelegramMediaThreadPickerPendingTTL    = 24 * time.Hour
	telegramMediaThreadPickerExpiredStatus = "cleared"
)

type TelegramMediaThreadPicker struct {
	ChatID                int64
	PickerMessageID       int64
	SourceMessageID       int64
	SourceIngressSurface  string
	SourceIngressUpdateID int64
	Inbound               core.InboundMessage
	Status                string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (s *SQLiteStore) RecordTelegramMediaThreadPicker(chatID int64, pickerMessageID int64, inbound core.InboundMessage, at time.Time) error {
	if s == nil || s.db == nil || chatID == 0 || pickerMessageID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := s.ExpireTelegramMediaThreadPickers(at.Add(-TelegramMediaThreadPickerPendingTTL), at); err != nil {
		return err
	}
	inbound = sanitizeTelegramMediaPickerInbound(inbound)
	raw, err := json.Marshal(inbound)
	if err != nil {
		return fmt.Errorf("marshal telegram media picker inbound: %w", err)
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin telegram media thread picker record: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	sourceSurface := strings.TrimSpace(inbound.IngressSurface)
	sourceUpdateID := inbound.IngressUpdateID
	if _, err := tx.Exec(`INSERT INTO telegram_media_thread_pickers(chat_id, picker_message_id, source_message_id, source_ingress_surface, source_ingress_update_id, inbound_json, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)
		ON CONFLICT(chat_id, picker_message_id) DO UPDATE SET
			source_message_id = excluded.source_message_id,
			source_ingress_surface = excluded.source_ingress_surface,
			source_ingress_update_id = excluded.source_ingress_update_id,
			inbound_json = excluded.inbound_json,
			status = 'pending',
			updated_at = excluded.updated_at`, chatID, pickerMessageID, inbound.MessageID, sourceSurface, sourceUpdateID, string(raw), stamp, stamp); err != nil {
		return fmt.Errorf("record telegram media thread picker: %w", err)
	}
	if sourceSurface != "" && sourceUpdateID > 0 {
		if err := markTelegramIngressParkedExec(tx, sourceSurface, sourceUpdateID, TelegramIngressParkReasonMediaThreadPicker, at); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit telegram media thread picker record: %w", err)
	}
	committed = true
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
	row := s.db.QueryRow(`SELECT chat_id, picker_message_id, source_message_id, source_ingress_surface, source_ingress_update_id, inbound_json, status, created_at, updated_at
		FROM telegram_media_thread_pickers WHERE chat_id = ? AND picker_message_id = ?`, chatID, pickerMessageID)
	var rec TelegramMediaThreadPicker
	var raw, created, updated string
	if err := row.Scan(&rec.ChatID, &rec.PickerMessageID, &rec.SourceMessageID, &rec.SourceIngressSurface, &rec.SourceIngressUpdateID, &raw, &rec.Status, &created, &updated); err != nil {
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

func (s *SQLiteStore) ExpireTelegramMediaThreadPickers(before time.Time, at time.Time) (int64, error) {
	if s == nil || s.db == nil || before.IsZero() {
		return 0, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin expire telegram media thread pickers: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	hasSourceColumns, err := telegramMediaPickerHasSourceIngressColumns(tx)
	if err != nil {
		return 0, err
	}
	query := `SELECT chat_id, source_message_id, source_ingress_surface, source_ingress_update_id, inbound_json FROM telegram_media_thread_pickers WHERE status = 'pending' AND updated_at < ?`
	if !hasSourceColumns {
		query = `SELECT chat_id, source_message_id, inbound_json FROM telegram_media_thread_pickers WHERE status = 'pending' AND updated_at < ?`
	}
	rows, err := tx.Query(query, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("query stale telegram media thread pickers: %w", err)
	}
	var staleSources []telegramMediaThreadPickerSource
	for rows.Next() {
		source, err := scanTelegramMediaThreadPickerSource(rows, hasSourceColumns)
		if err != nil {
			return 0, err
		}
		staleSources = append(staleSources, source)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close stale telegram media thread pickers: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale telegram media thread pickers: %w", err)
	}
	result, err := tx.Exec(`UPDATE telegram_media_thread_pickers SET status = ?, updated_at = ? WHERE status = 'pending' AND updated_at < ?`, telegramMediaThreadPickerExpiredStatus, at.UTC().Format(time.RFC3339Nano), before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("expire telegram media thread pickers: %w", err)
	}
	count, _ := result.RowsAffected()
	for _, source := range staleSources {
		if err := dropExpiredTelegramMediaThreadPickerSourceIngress(tx, source, at); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit expire telegram media thread pickers: %w", err)
	}
	committed = true
	return count, nil
}

type telegramMediaThreadPickerSource struct {
	ChatID                int64
	SourceMessageID       int64
	SourceIngressSurface  string
	SourceIngressUpdateID int64
}

func telegramMediaPickerHasSourceIngressColumns(tx *sql.Tx) (bool, error) {
	surfaceExists, err := schemaColumnExists(tx, "telegram_media_thread_pickers", "source_ingress_surface")
	if err != nil {
		return false, err
	}
	updateIDExists, err := schemaColumnExists(tx, "telegram_media_thread_pickers", "source_ingress_update_id")
	if err != nil {
		return false, err
	}
	return surfaceExists && updateIDExists, nil
}

func scanTelegramMediaThreadPickerSource(rows *sql.Rows, hasSourceColumns bool) (telegramMediaThreadPickerSource, error) {
	var source telegramMediaThreadPickerSource
	var raw string
	if hasSourceColumns {
		if err := rows.Scan(&source.ChatID, &source.SourceMessageID, &source.SourceIngressSurface, &source.SourceIngressUpdateID, &raw); err != nil {
			_ = rows.Close()
			return telegramMediaThreadPickerSource{}, fmt.Errorf("scan stale telegram media thread picker: %w", err)
		}
	} else if err := rows.Scan(&source.ChatID, &source.SourceMessageID, &raw); err != nil {
		_ = rows.Close()
		return telegramMediaThreadPickerSource{}, fmt.Errorf("scan legacy stale telegram media thread picker: %w", err)
	}
	if strings.TrimSpace(source.SourceIngressSurface) == "" || source.SourceIngressUpdateID <= 0 {
		var inbound core.InboundMessage
		if err := json.Unmarshal([]byte(raw), &inbound); err == nil {
			source.SourceIngressSurface = inbound.IngressSurface
			source.SourceIngressUpdateID = inbound.IngressUpdateID
		}
	}
	return source, nil
}

func dropExpiredTelegramMediaThreadPickerSourceIngress(exec telegramIngressExecutor, source telegramMediaThreadPickerSource, at time.Time) error {
	if strings.TrimSpace(source.SourceIngressSurface) != "" && source.SourceIngressUpdateID > 0 {
		return markTelegramIngressDroppedIfParkedExec(exec, source.SourceIngressSurface, source.SourceIngressUpdateID, TelegramIngressDropReasonMediaThreadPickerTTL, at)
	}
	return dropParkedTelegramIngressForMediaMessageExec(exec, source.ChatID, source.SourceMessageID, TelegramIngressDropReasonMediaThreadPickerTTL, at)
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
