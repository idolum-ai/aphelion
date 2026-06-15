//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) RecordTelegramIngressAccepted(record TelegramIngressUpdateRecord) (TelegramIngressTransitionResult, error) {
	record.Surface = strings.TrimSpace(record.Surface)
	if record.Surface == "" {
		return TelegramIngressTransitionResult{}, fmt.Errorf("telegram ingress update surface is required")
	}
	if record.UpdateID <= 0 {
		return TelegramIngressTransitionResult{}, fmt.Errorf("telegram ingress update id is required")
	}
	if record.AcceptedAt.IsZero() {
		record.AcceptedAt = time.Now().UTC()
	}
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, record.AcceptedAt)
	status := normalizeTelegramIngressUpdateStatus(record.Status)
	if status == "" {
		status = TelegramIngressUpdateAccepted
	}
	_, err := s.db.Exec(`
		INSERT INTO telegram_ingress_updates(
			surface, update_id, update_kind, chat_id, sender_id, message_id, session_id,
			status, turn_run_id, error_text, inbound_json, payload_json,
			accepted_at, queued_at, started_at, completed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(surface, update_id) DO UPDATE SET
			update_kind = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.update_kind
				ELSE excluded.update_kind
			END,
			chat_id = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.chat_id
				ELSE excluded.chat_id
			END,
			sender_id = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.sender_id
				ELSE excluded.sender_id
			END,
			message_id = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.message_id
				ELSE excluded.message_id
			END,
			session_id = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.session_id
				ELSE excluded.session_id
			END,
			inbound_json = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.inbound_json
				WHEN excluded.inbound_json != '' THEN excluded.inbound_json
				ELSE telegram_ingress_updates.inbound_json
			END,
			payload_json = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.payload_json
				WHEN excluded.payload_json != '' THEN excluded.payload_json
				ELSE telegram_ingress_updates.payload_json
			END,
			status = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'queued', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.status
				ELSE excluded.status
			END,
			updated_at = CASE
				WHEN telegram_ingress_updates.status IN ('parked', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.updated_at
				ELSE excluded.updated_at
			END
	`,
		record.Surface,
		record.UpdateID,
		strings.TrimSpace(record.UpdateKind),
		record.ChatID,
		record.SenderID,
		record.MessageID,
		strings.TrimSpace(record.SessionID),
		string(status),
		record.TurnRunID,
		clampStoreText(record.ErrorText, 2000),
		strings.TrimSpace(record.InboundJSON),
		clampStoreText(record.PayloadJSON, 20000),
		record.AcceptedAt.UTC().Format(time.RFC3339Nano),
		nullableTime(record.QueuedAt),
		nullableTime(record.StartedAt),
		nullableTime(record.CompletedAt),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return TelegramIngressTransitionResult{}, fmt.Errorf("record telegram ingress accepted: %w", err)
	}
	stored, ok, err := s.TelegramIngressUpdate(record.Surface, record.UpdateID)
	if err != nil {
		return TelegramIngressTransitionResult{}, err
	}
	if !ok {
		return TelegramIngressTransitionResult{}, fmt.Errorf("telegram ingress update %s/%d missing after accept", record.Surface, record.UpdateID)
	}
	return telegramIngressTransitionResult(stored, true), nil
}

func (s *SQLiteStore) MarkTelegramIngressParked(surface string, updateID int64, reason string, parkedAt time.Time) (TelegramIngressTransitionResult, error) {
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return TelegramIngressTransitionResult{}, nil
	}
	if parkedAt.IsZero() {
		parkedAt = time.Now().UTC()
	}
	if err := markTelegramIngressParkedExec(s.db, surface, updateID, reason, parkedAt); err != nil {
		return TelegramIngressTransitionResult{}, err
	}
	stored, ok, err := s.TelegramIngressUpdate(surface, updateID)
	if err != nil {
		return TelegramIngressTransitionResult{}, err
	}
	return telegramIngressTransitionResult(stored, ok), nil
}

func markTelegramIngressParkedExec(exec telegramIngressExecutor, surface string, updateID int64, reason string, parkedAt time.Time) error {
	if exec == nil {
		return nil
	}
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return nil
	}
	if parkedAt.IsZero() {
		parkedAt = time.Now().UTC()
	}
	parkedRaw := parkedAt.UTC().Format(time.RFC3339Nano)
	reason = clampStoreText(reason, 2000)
	_, err := exec.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = CASE
				WHEN status IN ('queued', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN status
				ELSE ?
			END,
			error_text = CASE
				WHEN status IN ('queued', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN error_text
				WHEN ? != '' THEN ?
				ELSE error_text
			END,
			updated_at = CASE
				WHEN status IN ('queued', 'running', 'completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN updated_at
				ELSE ?
			END
		WHERE surface = ? AND update_id = ?
	`,
		string(TelegramIngressUpdateParked),
		reason,
		reason,
		parkedRaw,
		surface,
		updateID,
	)
	if err != nil {
		return fmt.Errorf("mark telegram ingress parked: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MarkTelegramIngressQueued(surface string, updateID int64, queuedAt time.Time) (TelegramIngressTransitionResult, error) {
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return TelegramIngressTransitionResult{}, nil
	}
	if queuedAt.IsZero() {
		queuedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = CASE
				WHEN status IN ('completed', 'failed', 'dropped', 'interrupted', 'skipped', 'running') THEN status
				ELSE ?
			END,
			queued_at = CASE
				WHEN status IN ('completed', 'failed', 'dropped', 'interrupted', 'skipped', 'running') THEN queued_at
				ELSE COALESCE(queued_at, ?)
			END,
			updated_at = CASE
				WHEN status IN ('completed', 'failed', 'dropped', 'interrupted', 'skipped', 'running') THEN updated_at
				ELSE ?
			END
		WHERE surface = ? AND update_id = ?
	`, string(TelegramIngressUpdateQueued), queuedAt.UTC().Format(time.RFC3339Nano), queuedAt.UTC().Format(time.RFC3339Nano), surface, updateID)
	if err != nil {
		return TelegramIngressTransitionResult{}, fmt.Errorf("mark telegram ingress queued: %w", err)
	}
	stored, ok, err := s.TelegramIngressUpdate(surface, updateID)
	if err != nil {
		return TelegramIngressTransitionResult{}, err
	}
	return telegramIngressTransitionResult(stored, ok), nil
}

func (s *SQLiteStore) MarkTelegramIngressHandled(surface string, updateID int64, completedAt time.Time) error {
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = ?,
			completed_at = COALESCE(completed_at, ?),
			updated_at = ?
		WHERE surface = ? AND update_id = ? AND status = ?
	`, string(TelegramIngressUpdateCompleted), completedAt.UTC().Format(time.RFC3339Nano), completedAt.UTC().Format(time.RFC3339Nano), surface, updateID, string(TelegramIngressUpdateAccepted))
	if err != nil {
		return fmt.Errorf("mark telegram ingress handled: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MarkTelegramIngressFailed(surface string, updateID int64, errorText string, completedAt time.Time) error {
	return s.RecordTelegramIngressTerminal(TelegramIngressUpdateRecord{
		Surface:     surface,
		UpdateID:    updateID,
		Status:      TelegramIngressUpdateFailed,
		ErrorText:   errorText,
		CompletedAt: completedAt,
		UpdatedAt:   completedAt,
	})
}

func (s *SQLiteStore) MarkTelegramIngressCompleted(surface string, updateID int64, turnRunID int64, status TelegramIngressUpdateStatus, errorText string, completedAt time.Time) error {
	return s.RecordTelegramIngressTerminal(TelegramIngressUpdateRecord{
		Surface:     surface,
		UpdateID:    updateID,
		Status:      status,
		TurnRunID:   turnRunID,
		ErrorText:   errorText,
		CompletedAt: completedAt,
		UpdatedAt:   completedAt,
	})
}

func (s *SQLiteStore) DropPendingTelegramIngressForTelegramThread(chatID int64, threadID int64, reason string, completedAt time.Time) (int, error) {
	return dropPendingTelegramIngressForTelegramThreadExec(s.db, chatID, threadID, reason, completedAt)
}

type telegramIngressExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func dropPendingTelegramIngressForTelegramThreadTx(tx *sql.Tx, chatID int64, threadID int64, reason string, completedAt time.Time) (int, error) {
	return dropPendingTelegramIngressForTelegramThreadExec(tx, chatID, threadID, reason, completedAt)
}

func dropPendingTelegramIngressForTelegramThreadExec(exec telegramIngressExecutor, chatID int64, threadID int64, reason string, completedAt time.Time) (int, error) {
	if exec == nil || chatID == 0 || threadID <= 0 {
		return 0, nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = TelegramIngressDropReasonTelegramThreadClosed
	}
	threadSessionID := SessionIDForKey(SessionKey{ChatID: chatID, Scope: TelegramThreadScopeRef(chatID, threadID)})
	threadSessionPrefix := threadSessionID + "/"
	threadJSONNeedle := fmt.Sprintf("%%\"TelegramThreadID\":%d%%", threadID)
	threadJSONNeedleWithSpace := fmt.Sprintf("%%\"TelegramThreadID\": %d%%", threadID)
	completedRaw := completedAt.UTC().Format(time.RFC3339Nano)
	res, err := exec.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = ?,
			error_text = CASE
				WHEN ? != '' THEN ?
				ELSE error_text
			END,
			completed_at = COALESCE(completed_at, ?),
			updated_at = ?
		WHERE chat_id = ?
			AND status IN (?, ?)
			AND (
				session_id = ?
				OR SUBSTR(session_id, 1, ?) = ?
				OR inbound_json LIKE ?
				OR inbound_json LIKE ?
			)
	`,
		string(TelegramIngressUpdateDropped),
		clampStoreText(reason, 2000),
		clampStoreText(reason, 2000),
		completedRaw,
		completedRaw,
		chatID,
		string(TelegramIngressUpdateAccepted),
		string(TelegramIngressUpdateQueued),
		threadSessionID,
		len(threadSessionPrefix),
		threadSessionPrefix,
		threadJSONNeedle,
		threadJSONNeedleWithSpace,
	)
	if err != nil {
		return 0, fmt.Errorf("drop pending telegram ingress for thread: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("drop pending telegram ingress rows affected: %w", err)
	}
	return int(count), nil
}

func (s *SQLiteStore) MarkTelegramIngressDroppedIfDispatchable(surface string, updateID int64, reason string, completedAt time.Time) (TelegramIngressTransitionResult, error) {
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return TelegramIngressTransitionResult{}, nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	completedRaw := completedAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = ?,
			error_text = CASE
				WHEN ? != '' THEN ?
				ELSE error_text
			END,
			completed_at = COALESCE(completed_at, ?),
			updated_at = ?
		WHERE surface = ? AND update_id = ? AND status IN (?, ?)
	`,
		string(TelegramIngressUpdateDropped),
		clampStoreText(reason, 2000),
		clampStoreText(reason, 2000),
		completedRaw,
		completedRaw,
		surface,
		updateID,
		string(TelegramIngressUpdateAccepted),
		string(TelegramIngressUpdateQueued),
	)
	if err != nil {
		return TelegramIngressTransitionResult{}, fmt.Errorf("mark telegram ingress dropped: %w", err)
	}
	stored, ok, err := s.TelegramIngressUpdate(surface, updateID)
	if err != nil {
		return TelegramIngressTransitionResult{}, err
	}
	return telegramIngressTransitionResult(stored, ok), nil
}

func markTelegramIngressDroppedIfParkedExec(exec telegramIngressExecutor, surface string, updateID int64, reason string, completedAt time.Time) error {
	if exec == nil {
		return nil
	}
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	completedRaw := completedAt.UTC().Format(time.RFC3339Nano)
	reason = clampStoreText(reason, 2000)
	_, err := exec.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = ?,
			error_text = CASE
				WHEN ? != '' THEN ?
				ELSE error_text
			END,
			completed_at = COALESCE(completed_at, ?),
			updated_at = ?
		WHERE surface = ? AND update_id = ? AND status = ?
	`,
		string(TelegramIngressUpdateDropped),
		reason,
		reason,
		completedRaw,
		completedRaw,
		surface,
		updateID,
		string(TelegramIngressUpdateParked),
	)
	if err != nil {
		return fmt.Errorf("mark parked telegram ingress dropped: %w", err)
	}
	return nil
}

func dropParkedTelegramIngressForMediaMessageExec(exec telegramIngressExecutor, chatID int64, messageID int64, reason string, completedAt time.Time) error {
	if exec == nil || chatID == 0 || messageID <= 0 {
		return nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	completedRaw := completedAt.UTC().Format(time.RFC3339Nano)
	reason = clampStoreText(reason, 2000)
	_, err := exec.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = ?,
			error_text = CASE
				WHEN ? != '' THEN ?
				ELSE error_text
			END,
			completed_at = COALESCE(completed_at, ?),
			updated_at = ?
		WHERE chat_id = ?
			AND message_id = ?
			AND status = ?
			AND error_text = ?
	`,
		string(TelegramIngressUpdateDropped),
		reason,
		reason,
		completedRaw,
		completedRaw,
		chatID,
		messageID,
		string(TelegramIngressUpdateParked),
		TelegramIngressParkReasonMediaThreadPicker,
	)
	if err != nil {
		return fmt.Errorf("drop parked telegram ingress for media message: %w", err)
	}
	return nil
}

func (s *SQLiteStore) RecordTelegramIngressTerminal(record TelegramIngressUpdateRecord) error {
	record.Surface = strings.TrimSpace(record.Surface)
	if record.Surface == "" {
		return fmt.Errorf("telegram ingress terminal surface is required")
	}
	if record.UpdateID <= 0 {
		return fmt.Errorf("telegram ingress terminal update id is required")
	}
	record.Status = normalizeTelegramIngressUpdateStatus(record.Status)
	switch record.Status {
	case TelegramIngressUpdateCompleted, TelegramIngressUpdateFailed, TelegramIngressUpdateDropped, TelegramIngressUpdateInterrupted, TelegramIngressUpdateSkipped:
	default:
		return fmt.Errorf("invalid telegram ingress terminal status %q", record.Status)
	}
	if record.CompletedAt.IsZero() {
		record.CompletedAt = time.Now().UTC()
	}
	if record.AcceptedAt.IsZero() {
		record.AcceptedAt = record.CompletedAt
	}
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, record.CompletedAt)
	_, err := s.db.Exec(`
		INSERT INTO telegram_ingress_updates(
			surface, update_id, update_kind, chat_id, sender_id, message_id, session_id,
			status, turn_run_id, error_text, inbound_json, payload_json,
			accepted_at, queued_at, started_at, completed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(surface, update_id) DO UPDATE SET
			update_kind = CASE
				WHEN excluded.update_kind != '' THEN excluded.update_kind
				ELSE telegram_ingress_updates.update_kind
			END,
			chat_id = CASE
				WHEN excluded.chat_id != 0 THEN excluded.chat_id
				ELSE telegram_ingress_updates.chat_id
			END,
			sender_id = CASE
				WHEN excluded.sender_id != 0 THEN excluded.sender_id
				ELSE telegram_ingress_updates.sender_id
			END,
			message_id = CASE
				WHEN excluded.message_id != 0 THEN excluded.message_id
				ELSE telegram_ingress_updates.message_id
			END,
			session_id = CASE
				WHEN excluded.session_id != '' THEN excluded.session_id
				ELSE telegram_ingress_updates.session_id
			END,
			status = CASE
				WHEN telegram_ingress_updates.status IN ('completed', 'failed', 'dropped', 'interrupted', 'skipped') THEN telegram_ingress_updates.status
				ELSE excluded.status
			END,
			turn_run_id = CASE
				WHEN excluded.turn_run_id > 0 THEN excluded.turn_run_id
				ELSE telegram_ingress_updates.turn_run_id
			END,
			error_text = CASE
				WHEN excluded.error_text != '' THEN excluded.error_text
				ELSE telegram_ingress_updates.error_text
			END,
			inbound_json = CASE
				WHEN excluded.inbound_json != '' THEN excluded.inbound_json
				ELSE telegram_ingress_updates.inbound_json
			END,
			payload_json = CASE
				WHEN excluded.payload_json != '' THEN excluded.payload_json
				ELSE telegram_ingress_updates.payload_json
			END,
			completed_at = COALESCE(telegram_ingress_updates.completed_at, excluded.completed_at),
			updated_at = excluded.updated_at
	`,
		record.Surface,
		record.UpdateID,
		strings.TrimSpace(record.UpdateKind),
		record.ChatID,
		record.SenderID,
		record.MessageID,
		strings.TrimSpace(record.SessionID),
		string(record.Status),
		record.TurnRunID,
		clampStoreText(record.ErrorText, 2000),
		strings.TrimSpace(record.InboundJSON),
		clampStoreText(record.PayloadJSON, 20000),
		record.AcceptedAt.UTC().Format(time.RFC3339Nano),
		nullableTime(record.QueuedAt),
		nullableTime(record.StartedAt),
		nullableTime(record.CompletedAt),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("record telegram ingress terminal: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PendingTelegramIngressUpdates(surface string, limit int) ([]TelegramIngressUpdateRecord, error) {
	surface = strings.TrimSpace(surface)
	if surface == "" {
		return nil, fmt.Errorf("telegram ingress pending surface is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT surface, update_id, update_kind, chat_id, sender_id, message_id, session_id,
			status, turn_run_id, error_text, inbound_json, payload_json,
			accepted_at, queued_at, started_at, completed_at, updated_at
		FROM telegram_ingress_updates
		WHERE surface = ? AND status IN (?, ?)
		ORDER BY update_id ASC
		LIMIT ?
	`, surface, string(TelegramIngressUpdateAccepted), string(TelegramIngressUpdateQueued), limit)
	if err != nil {
		return nil, fmt.Errorf("query pending telegram ingress updates: %w", err)
	}
	defer rows.Close()
	return scanTelegramIngressUpdateRows(rows)
}

func (s *SQLiteStore) TelegramIngressUpdate(surface string, updateID int64) (TelegramIngressUpdateRecord, bool, error) {
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return TelegramIngressUpdateRecord{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT surface, update_id, update_kind, chat_id, sender_id, message_id, session_id,
			status, turn_run_id, error_text, inbound_json, payload_json,
			accepted_at, queued_at, started_at, completed_at, updated_at
		FROM telegram_ingress_updates
		WHERE surface = ? AND update_id = ?
	`, surface, updateID)
	record, err := scanTelegramIngressUpdate(row)
	if err == nil {
		return record, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return TelegramIngressUpdateRecord{}, false, nil
	}
	return TelegramIngressUpdateRecord{}, false, err
}

func (s *SQLiteStore) RecentTelegramIngressUpdates(limit int) ([]TelegramIngressUpdateRecord, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := s.db.Query(`
		SELECT surface, update_id, update_kind, chat_id, sender_id, message_id, session_id,
			status, turn_run_id, error_text, inbound_json, payload_json,
			accepted_at, queued_at, started_at, completed_at, updated_at
		FROM telegram_ingress_updates
		ORDER BY
			CASE WHEN status IN ('accepted', 'parked', 'queued', 'running') THEN 0 ELSE 1 END,
			updated_at DESC,
			update_id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent telegram ingress updates: %w", err)
	}
	defer rows.Close()
	return scanTelegramIngressUpdateRows(rows)
}

func (s *SQLiteStore) ReconcileRunningTelegramIngressWithTerminalTurnRuns() (int, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = CASE (
				SELECT tr.status
				FROM turn_runs tr
				WHERE tr.id = telegram_ingress_updates.turn_run_id
			)
				WHEN ? THEN ?
				WHEN ? THEN ?
				WHEN ? THEN ?
				ELSE status
			END,
			error_text = COALESCE(
				NULLIF(error_text, ''),
				NULLIF((
					SELECT tr.error_text
					FROM turn_runs tr
					WHERE tr.id = telegram_ingress_updates.turn_run_id
				), ''),
				''
			),
			completed_at = COALESCE(
				completed_at,
				NULLIF((
					SELECT tr.completed_at
					FROM turn_runs tr
					WHERE tr.id = telegram_ingress_updates.turn_run_id
				), ''),
				?
			),
			updated_at = ?
		WHERE status = ?
			AND turn_run_id > 0
			AND EXISTS (
				SELECT 1
				FROM turn_runs tr
				WHERE tr.id = telegram_ingress_updates.turn_run_id
					AND tr.status IN (?, ?, ?)
			)
	`,
		string(TurnRunStatusCompleted),
		string(TelegramIngressUpdateCompleted),
		string(TurnRunStatusFailed),
		string(TelegramIngressUpdateFailed),
		string(TurnRunStatusInterrupted),
		string(TelegramIngressUpdateInterrupted),
		now,
		now,
		string(TelegramIngressUpdateRunning),
		string(TurnRunStatusCompleted),
		string(TurnRunStatusFailed),
		string(TurnRunStatusInterrupted),
	)
	if err != nil {
		return 0, fmt.Errorf("reconcile running telegram ingress with terminal turn runs: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reconcile running telegram ingress rows affected: %w", err)
	}
	return int(count), nil
}
