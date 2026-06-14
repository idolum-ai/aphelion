//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
)

func scanTelegramIngressUpdateRows(rows *sql.Rows) ([]TelegramIngressUpdateRecord, error) {
	var out []TelegramIngressUpdateRecord
	for rows.Next() {
		record, err := scanTelegramIngressUpdate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telegram ingress updates: %w", err)
	}
	return out, nil
}

func scanTelegramIngressUpdate(scanner interface {
	Scan(dest ...any) error
}) (TelegramIngressUpdateRecord, error) {
	var record TelegramIngressUpdateRecord
	var (
		statusRaw      string
		acceptedAtRaw  string
		queuedAtRaw    sql.NullString
		startedAtRaw   sql.NullString
		completedAtRaw sql.NullString
		updatedAtRaw   string
	)
	if err := scanner.Scan(
		&record.Surface,
		&record.UpdateID,
		&record.UpdateKind,
		&record.ChatID,
		&record.SenderID,
		&record.MessageID,
		&record.SessionID,
		&statusRaw,
		&record.TurnRunID,
		&record.ErrorText,
		&record.InboundJSON,
		&record.PayloadJSON,
		&acceptedAtRaw,
		&queuedAtRaw,
		&startedAtRaw,
		&completedAtRaw,
		&updatedAtRaw,
	); err != nil {
		return TelegramIngressUpdateRecord{}, fmt.Errorf("scan telegram ingress update: %w", err)
	}
	acceptedAt, err := parseSQLiteTime(acceptedAtRaw)
	if err != nil {
		return TelegramIngressUpdateRecord{}, fmt.Errorf("parse telegram ingress accepted_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return TelegramIngressUpdateRecord{}, fmt.Errorf("parse telegram ingress updated_at: %w", err)
	}
	record.Status = normalizeTelegramIngressUpdateStatus(TelegramIngressUpdateStatus(statusRaw))
	record.AcceptedAt = acceptedAt
	record.UpdatedAt = updatedAt
	if queuedAtRaw.Valid && strings.TrimSpace(queuedAtRaw.String) != "" {
		t, err := parseSQLiteTime(queuedAtRaw.String)
		if err != nil {
			return TelegramIngressUpdateRecord{}, fmt.Errorf("parse telegram ingress queued_at: %w", err)
		}
		record.QueuedAt = t
	}
	if startedAtRaw.Valid && strings.TrimSpace(startedAtRaw.String) != "" {
		t, err := parseSQLiteTime(startedAtRaw.String)
		if err != nil {
			return TelegramIngressUpdateRecord{}, fmt.Errorf("parse telegram ingress started_at: %w", err)
		}
		record.StartedAt = t
	}
	if completedAtRaw.Valid && strings.TrimSpace(completedAtRaw.String) != "" {
		t, err := parseSQLiteTime(completedAtRaw.String)
		if err != nil {
			return TelegramIngressUpdateRecord{}, fmt.Errorf("parse telegram ingress completed_at: %w", err)
		}
		record.CompletedAt = t
	}
	return record, nil
}

func normalizeTelegramIngressUpdateStatus(status TelegramIngressUpdateStatus) TelegramIngressUpdateStatus {
	switch TelegramIngressUpdateStatus(strings.TrimSpace(string(status))) {
	case TelegramIngressUpdateAccepted:
		return TelegramIngressUpdateAccepted
	case TelegramIngressUpdateParked:
		return TelegramIngressUpdateParked
	case TelegramIngressUpdateQueued:
		return TelegramIngressUpdateQueued
	case TelegramIngressUpdateRunning:
		return TelegramIngressUpdateRunning
	case TelegramIngressUpdateCompleted:
		return TelegramIngressUpdateCompleted
	case TelegramIngressUpdateFailed:
		return TelegramIngressUpdateFailed
	case TelegramIngressUpdateDropped:
		return TelegramIngressUpdateDropped
	case TelegramIngressUpdateInterrupted:
		return TelegramIngressUpdateInterrupted
	case TelegramIngressUpdateSkipped:
		return TelegramIngressUpdateSkipped
	default:
		return ""
	}
}

func clampStoreText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}
