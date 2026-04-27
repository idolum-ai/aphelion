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

type ModelSlotOverrideRecord struct {
	ID             int64
	Slot           string
	Config         core.ModelSlotConfig
	PreviousConfig core.ModelSlotConfig
	Status         string
	CreatedBy      string
	Reason         string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (s *SQLiteStore) ActiveModelSlotOverride(slot string, now time.Time) (ModelSlotOverrideRecord, bool, error) {
	if s == nil {
		return ModelSlotOverrideRecord{}, false, fmt.Errorf("store is nil")
	}
	slot = core.NormalizeModelSlot(slot)
	if slot == "" {
		return ModelSlotOverrideRecord{}, false, fmt.Errorf("model slot is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.db.Query(`
		SELECT id, slot, config_json, previous_config_json, status, created_by, reason, expires_at, created_at, updated_at
		FROM model_slot_overrides
		WHERE slot = ? AND status = 'active'
		ORDER BY id DESC
		LIMIT 10
	`, slot)
	if err != nil {
		return ModelSlotOverrideRecord{}, false, fmt.Errorf("query model slot override: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		record, err := scanModelSlotOverride(rows)
		if err != nil {
			return ModelSlotOverrideRecord{}, false, err
		}
		if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now.UTC()) {
			continue
		}
		return record, true, nil
	}
	if err := rows.Err(); err != nil {
		return ModelSlotOverrideRecord{}, false, fmt.Errorf("iterate model slot override: %w", err)
	}
	return ModelSlotOverrideRecord{}, false, nil
}

func (s *SQLiteStore) SetModelSlotOverride(record ModelSlotOverrideRecord) (ModelSlotOverrideRecord, error) {
	if s == nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("store is nil")
	}
	record.Slot = core.NormalizeModelSlot(firstNonEmptyModelSlot(record.Slot, record.Config.Slot))
	if record.Slot == "" {
		return ModelSlotOverrideRecord{}, fmt.Errorf("model slot is required")
	}
	record.Config = core.NormalizeModelSlotConfig(record.Config)
	record.Config.Slot = record.Slot
	record.PreviousConfig = core.NormalizeModelSlotConfig(record.PreviousConfig)
	if record.PreviousConfig.Slot == "" {
		record.PreviousConfig.Slot = record.Slot
	}
	record.Status = "active"
	record.CreatedBy = strings.TrimSpace(record.CreatedBy)
	record.Reason = strings.TrimSpace(record.Reason)
	now := time.Now().UTC()
	record.CreatedAt = nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	record.UpdatedAt = record.CreatedAt
	configJSON, err := json.Marshal(record.Config)
	if err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("marshal model slot config: %w", err)
	}
	previousJSON, err := json.Marshal(record.PreviousConfig)
	if err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("marshal previous model slot config: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("begin model slot override tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		UPDATE model_slot_overrides
		SET status = 'superseded', updated_at = ?
		WHERE slot = ? AND status = 'active'
	`, record.CreatedAt.Format(time.RFC3339Nano), record.Slot); err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("supersede model slot overrides: %w", err)
	}
	res, err := tx.Exec(`
		INSERT INTO model_slot_overrides (
			slot, config_json, previous_config_json, status, created_by, reason, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, 'active', ?, ?, ?, ?, ?)
	`,
		record.Slot,
		string(configJSON),
		string(previousJSON),
		record.CreatedBy,
		record.Reason,
		nullableTimeRFC3339(record.ExpiresAt),
		record.CreatedAt.Format(time.RFC3339Nano),
		record.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("insert model slot override: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("model slot override id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("commit model slot override tx: %w", err)
	}
	record.ID = id
	return record, nil
}

func (s *SQLiteStore) ClearModelSlotOverride(slot string, status string, now time.Time) (ModelSlotOverrideRecord, bool, error) {
	if s == nil {
		return ModelSlotOverrideRecord{}, false, fmt.Errorf("store is nil")
	}
	slot = core.NormalizeModelSlot(slot)
	if slot == "" {
		return ModelSlotOverrideRecord{}, false, fmt.Errorf("model slot is required")
	}
	switch strings.TrimSpace(status) {
	case "cleared", "rolled_back", "expired":
	default:
		status = "cleared"
	}
	active, ok, err := s.ActiveModelSlotOverride(slot, now)
	if err != nil || !ok {
		return ModelSlotOverrideRecord{}, ok, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if _, err := s.db.Exec(`
		UPDATE model_slot_overrides
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = 'active'
	`, status, now.UTC().Format(time.RFC3339Nano), active.ID); err != nil {
		return ModelSlotOverrideRecord{}, false, fmt.Errorf("clear model slot override: %w", err)
	}
	active.Status = status
	active.UpdatedAt = now.UTC()
	return active, true, nil
}

func (s *SQLiteStore) ExpireModelSlotOverrides(now time.Time) ([]ModelSlotOverrideRecord, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.db.Query(`
		SELECT id, slot, config_json, previous_config_json, status, created_by, reason, expires_at, created_at, updated_at
		FROM model_slot_overrides
		WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at != '' AND expires_at <= ?
		ORDER BY id ASC
	`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query expired model slot overrides: %w", err)
	}
	var expired []ModelSlotOverrideRecord
	for rows.Next() {
		record, err := scanModelSlotOverride(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate expired model slot overrides: %w", err)
	}
	rows.Close()
	if len(expired) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(expired)+1)
	placeholders := make([]string, 0, len(expired))
	for _, record := range expired {
		placeholders = append(placeholders, "?")
		args = append(args, record.ID)
	}
	query := fmt.Sprintf(`
		UPDATE model_slot_overrides
		SET status = 'expired', updated_at = ?
		WHERE id IN (%s) AND status = 'active'
	`, strings.Join(placeholders, ","))
	args = append([]any{now.UTC().Format(time.RFC3339Nano)}, args...)
	if _, err := s.db.Exec(query, args...); err != nil {
		return nil, fmt.Errorf("mark model slot overrides expired: %w", err)
	}
	return expired, nil
}

func (s *SQLiteStore) ModelSlotOverrideHistory(slot string, limit int) ([]ModelSlotOverrideRecord, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	slot = core.NormalizeModelSlot(slot)
	if limit <= 0 {
		limit = 20
	}
	var (
		rows *sql.Rows
		err  error
	)
	if slot == "" {
		rows, err = s.db.Query(`
			SELECT id, slot, config_json, previous_config_json, status, created_by, reason, expires_at, created_at, updated_at
			FROM model_slot_overrides
			ORDER BY id DESC
			LIMIT ?
		`, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT id, slot, config_json, previous_config_json, status, created_by, reason, expires_at, created_at, updated_at
			FROM model_slot_overrides
			WHERE slot = ?
			ORDER BY id DESC
			LIMIT ?
		`, slot, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query model slot override history: %w", err)
	}
	defer rows.Close()
	records := make([]ModelSlotOverrideRecord, 0, limit)
	for rows.Next() {
		record, err := scanModelSlotOverride(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model slot override history: %w", err)
	}
	return records, nil
}

func scanModelSlotOverride(rows interface {
	Scan(dest ...any) error
}) (ModelSlotOverrideRecord, error) {
	var record ModelSlotOverrideRecord
	var configRaw, previousRaw string
	var expiresRaw sql.NullString
	var createdRaw, updatedRaw string
	if err := rows.Scan(
		&record.ID,
		&record.Slot,
		&configRaw,
		&previousRaw,
		&record.Status,
		&record.CreatedBy,
		&record.Reason,
		&expiresRaw,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("scan model slot override: %w", err)
	}
	if err := json.Unmarshal([]byte(firstNonEmptyModelSlot(configRaw, "{}")), &record.Config); err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("decode model slot config: %w", err)
	}
	if err := json.Unmarshal([]byte(firstNonEmptyModelSlot(previousRaw, "{}")), &record.PreviousConfig); err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("decode previous model slot config: %w", err)
	}
	if expiresRaw.Valid && strings.TrimSpace(expiresRaw.String) != "" {
		expiresAt, err := parseSQLiteTime(expiresRaw.String)
		if err != nil {
			return ModelSlotOverrideRecord{}, fmt.Errorf("parse model slot expires_at: %w", err)
		}
		record.ExpiresAt = expiresAt
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("parse model slot created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return ModelSlotOverrideRecord{}, fmt.Errorf("parse model slot updated_at: %w", err)
	}
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	record.Slot = core.NormalizeModelSlot(record.Slot)
	record.Config = core.NormalizeModelSlotConfig(record.Config)
	record.PreviousConfig = core.NormalizeModelSlotConfig(record.PreviousConfig)
	return record, nil
}

func firstNonEmptyModelSlot(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
