//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	TailnetGrantBindingStatusProposed = "proposed"
	TailnetGrantBindingStatusApplied  = "applied"
	TailnetGrantBindingStatusDrifted  = "drifted"
	TailnetGrantBindingStatusRevoked  = "revoked"
	TailnetGrantBindingStatusFailed   = "failed"
)

type TailnetGrantBinding struct {
	BindingID          string
	GrantID            string
	SurfaceID          string
	GrantedTo          string
	CapabilityKind     string
	TargetResource     string
	DesiredPolicyJSON  string
	AppliedPolicyHash  string
	ObservedPolicyHash string
	Status             string
	DriftReason        string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	AppliedAt          time.Time
	RevokedAt          time.Time
}

type TailnetGrantBindingEvent struct {
	ID        int64
	BindingID string
	EventType string
	Status    string
	Detail    string
	CreatedAt time.Time
}

type TailnetGrantBindingFilter struct {
	GrantID   string
	SurfaceID string
	Status    string
	Limit     int
}

func (s *SQLiteStore) UpsertTailnetGrantBinding(binding TailnetGrantBinding) (TailnetGrantBinding, error) {
	if s == nil {
		return TailnetGrantBinding{}, fmt.Errorf("store is nil")
	}
	binding = NormalizeTailnetGrantBinding(binding)
	if binding.BindingID == "" {
		return TailnetGrantBinding{}, fmt.Errorf("tailnet grant binding id is required")
	}
	if binding.GrantID == "" {
		return TailnetGrantBinding{}, fmt.Errorf("tailnet grant binding grant_id is required")
	}
	if binding.SurfaceID == "" {
		return TailnetGrantBinding{}, fmt.Errorf("tailnet grant binding surface_id is required")
	}
	if binding.Status == TailnetGrantBindingStatusApplied && binding.AppliedPolicyHash == "" {
		return TailnetGrantBinding{}, fmt.Errorf("tailnet grant binding applied status requires applied_policy_hash evidence")
	}
	if binding.Status == TailnetGrantBindingStatusDrifted && binding.DriftReason == "" {
		return TailnetGrantBinding{}, fmt.Errorf("tailnet grant binding drifted status requires drift_reason evidence")
	}
	now := time.Now().UTC()
	binding.CreatedAt = nonZeroTimeOrNow(binding.CreatedAt, now).UTC()
	binding.UpdatedAt = nonZeroTimeOrNow(binding.UpdatedAt, now).UTC()
	if binding.Status == TailnetGrantBindingStatusApplied && binding.AppliedAt.IsZero() {
		binding.AppliedAt = binding.UpdatedAt
	}
	if binding.Status != TailnetGrantBindingStatusRevoked {
		binding.RevokedAt = time.Time{}
	}
	previous, previousOK, err := s.TailnetGrantBinding(binding.BindingID)
	if err != nil {
		return TailnetGrantBinding{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TailnetGrantBinding{}, fmt.Errorf("begin tailnet grant binding tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO tailnet_grant_bindings(
			binding_id, grant_id, surface_id, granted_to, capability_kind, target_resource,
			desired_policy_json, applied_policy_hash, observed_policy_hash, status, drift_reason,
			created_at, updated_at, applied_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(binding_id) DO UPDATE SET
			grant_id = excluded.grant_id,
			surface_id = excluded.surface_id,
			granted_to = excluded.granted_to,
			capability_kind = excluded.capability_kind,
			target_resource = excluded.target_resource,
			desired_policy_json = excluded.desired_policy_json,
			applied_policy_hash = excluded.applied_policy_hash,
			observed_policy_hash = excluded.observed_policy_hash,
			status = excluded.status,
			drift_reason = excluded.drift_reason,
			updated_at = excluded.updated_at,
			applied_at = excluded.applied_at,
			revoked_at = excluded.revoked_at
	`, binding.BindingID, binding.GrantID, binding.SurfaceID, binding.GrantedTo, binding.CapabilityKind, binding.TargetResource,
		binding.DesiredPolicyJSON, binding.AppliedPolicyHash, binding.ObservedPolicyHash, binding.Status, binding.DriftReason,
		binding.CreatedAt.Format(time.RFC3339Nano), binding.UpdatedAt.Format(time.RFC3339Nano), nullableTimeRFC3339(binding.AppliedAt), nullableTimeRFC3339(binding.RevokedAt)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return TailnetGrantBinding{}, fmt.Errorf("tailnet grant binding already exists for grant %q and surface %q", binding.GrantID, binding.SurfaceID)
		}
		return TailnetGrantBinding{}, fmt.Errorf("upsert tailnet grant binding: %w", err)
	}
	if eventType, detail := tailnetGrantBindingEventForUpsert(previous, previousOK, binding); eventType != "" {
		if _, err := tx.Exec(`
			INSERT INTO tailnet_grant_binding_events(binding_id, event_type, status, detail, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, binding.BindingID, eventType, binding.Status, detail, binding.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			return TailnetGrantBinding{}, fmt.Errorf("append tailnet grant binding event: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return TailnetGrantBinding{}, fmt.Errorf("commit tailnet grant binding tx: %w", err)
	}
	stored, ok, err := s.TailnetGrantBinding(binding.BindingID)
	if err != nil {
		return TailnetGrantBinding{}, err
	}
	if !ok {
		return TailnetGrantBinding{}, fmt.Errorf("tailnet grant binding %q not found after upsert", binding.BindingID)
	}
	return stored, nil
}

func (s *SQLiteStore) TailnetGrantBinding(bindingID string) (TailnetGrantBinding, bool, error) {
	if s == nil {
		return TailnetGrantBinding{}, false, fmt.Errorf("store is nil")
	}
	bindingID = strings.TrimSpace(bindingID)
	if bindingID == "" {
		return TailnetGrantBinding{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT binding_id, grant_id, surface_id, granted_to, capability_kind, target_resource,
			desired_policy_json, applied_policy_hash, observed_policy_hash, status, drift_reason,
			created_at, updated_at, applied_at, revoked_at
		FROM tailnet_grant_bindings
		WHERE binding_id = ?
	`, bindingID)
	record, err := scanTailnetGrantBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TailnetGrantBinding{}, false, nil
	}
	if err != nil {
		return TailnetGrantBinding{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) TailnetGrantBindings(filter TailnetGrantBindingFilter) ([]TailnetGrantBinding, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	filter.GrantID = strings.TrimSpace(filter.GrantID)
	filter.SurfaceID = strings.TrimSpace(filter.SurfaceID)
	filter.Status = normalizeTailnetGrantBindingStatus(filter.Status)
	query := `
		SELECT binding_id, grant_id, surface_id, granted_to, capability_kind, target_resource,
			desired_policy_json, applied_policy_hash, observed_policy_hash, status, drift_reason,
			created_at, updated_at, applied_at, revoked_at
		FROM tailnet_grant_bindings
	`
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 3)
	if filter.GrantID != "" {
		clauses = append(clauses, "grant_id = ?")
		args = append(args, filter.GrantID)
	}
	if filter.SurfaceID != "" {
		clauses = append(clauses, "surface_id = ?")
		args = append(args, filter.SurfaceID)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, filter.Status)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += ` ORDER BY CASE status
		WHEN 'drifted' THEN 0
		WHEN 'failed' THEN 1
		WHEN 'proposed' THEN 2
		WHEN 'applied' THEN 3
		WHEN 'revoked' THEN 4
		ELSE 5
	END, updated_at DESC, binding_id ASC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tailnet grant bindings: %w", err)
	}
	defer rows.Close()
	out := make([]TailnetGrantBinding, 0, filter.Limit)
	for rows.Next() {
		record, err := scanTailnetGrantBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tailnet grant bindings: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) ApplyTailnetGrantBinding(bindingID string, appliedPolicyHash string, observedPolicyHash string, now time.Time) (TailnetGrantBinding, bool, error) {
	current, ok, err := s.TailnetGrantBinding(bindingID)
	if err != nil || !ok {
		return TailnetGrantBinding{}, ok, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	current.Status = TailnetGrantBindingStatusApplied
	current.AppliedPolicyHash = strings.TrimSpace(appliedPolicyHash)
	current.ObservedPolicyHash = strings.TrimSpace(observedPolicyHash)
	current.DriftReason = ""
	current.AppliedAt = now.UTC()
	current.UpdatedAt = now.UTC()
	stored, err := s.UpsertTailnetGrantBinding(current)
	if err != nil {
		return TailnetGrantBinding{}, false, err
	}
	return stored, true, nil
}

func (s *SQLiteStore) DriftTailnetGrantBinding(bindingID string, reason string, observedPolicyHash string, now time.Time) (TailnetGrantBinding, bool, error) {
	current, ok, err := s.TailnetGrantBinding(bindingID)
	if err != nil || !ok {
		return TailnetGrantBinding{}, ok, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	current.Status = TailnetGrantBindingStatusDrifted
	current.DriftReason = strings.TrimSpace(reason)
	current.ObservedPolicyHash = strings.TrimSpace(observedPolicyHash)
	current.UpdatedAt = now.UTC()
	stored, err := s.UpsertTailnetGrantBinding(current)
	if err != nil {
		return TailnetGrantBinding{}, false, err
	}
	return stored, true, nil
}

func (s *SQLiteStore) RevokeTailnetGrantBinding(bindingID string, reason string, now time.Time) (TailnetGrantBinding, bool, error) {
	current, ok, err := s.TailnetGrantBinding(bindingID)
	if err != nil || !ok {
		return TailnetGrantBinding{}, ok, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if current.Status == TailnetGrantBindingStatusRevoked {
		return current, true, nil
	}
	current.Status = TailnetGrantBindingStatusRevoked
	current.DriftReason = strings.TrimSpace(reason)
	current.RevokedAt = now.UTC()
	current.UpdatedAt = now.UTC()
	stored, err := s.UpsertTailnetGrantBinding(current)
	if err != nil {
		return TailnetGrantBinding{}, false, err
	}
	return stored, true, nil
}

func (s *SQLiteStore) TailnetGrantBindingEvents(bindingID string, limit int) ([]TailnetGrantBindingEvent, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	bindingID = strings.TrimSpace(bindingID)
	if bindingID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, binding_id, event_type, status, detail, created_at
		FROM tailnet_grant_binding_events
		WHERE binding_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, bindingID, limit)
	if err != nil {
		return nil, fmt.Errorf("query tailnet grant binding events: %w", err)
	}
	defer rows.Close()
	out := make([]TailnetGrantBindingEvent, 0, limit)
	for rows.Next() {
		var event TailnetGrantBindingEvent
		var createdAtRaw string
		if err := rows.Scan(&event.ID, &event.BindingID, &event.EventType, &event.Status, &event.Detail, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan tailnet grant binding event: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, err
		}
		event.CreatedAt = createdAt
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tailnet grant binding events: %w", err)
	}
	return out, nil
}

func NormalizeTailnetGrantBinding(binding TailnetGrantBinding) TailnetGrantBinding {
	binding.BindingID = strings.TrimSpace(binding.BindingID)
	binding.GrantID = strings.TrimSpace(binding.GrantID)
	binding.SurfaceID = strings.TrimSpace(binding.SurfaceID)
	binding.GrantedTo = strings.TrimSpace(binding.GrantedTo)
	binding.CapabilityKind = strings.TrimSpace(binding.CapabilityKind)
	binding.TargetResource = strings.TrimSpace(binding.TargetResource)
	binding.DesiredPolicyJSON = strings.TrimSpace(binding.DesiredPolicyJSON)
	if binding.DesiredPolicyJSON == "" {
		binding.DesiredPolicyJSON = "{}"
	}
	binding.AppliedPolicyHash = strings.TrimSpace(binding.AppliedPolicyHash)
	binding.ObservedPolicyHash = strings.TrimSpace(binding.ObservedPolicyHash)
	binding.Status = normalizeTailnetGrantBindingStatus(binding.Status)
	if binding.Status == "" {
		binding.Status = TailnetGrantBindingStatusProposed
	}
	binding.DriftReason = strings.TrimSpace(binding.DriftReason)
	return binding
}

func scanTailnetGrantBinding(scanner interface{ Scan(dest ...any) error }) (TailnetGrantBinding, error) {
	var record TailnetGrantBinding
	var createdAtRaw, updatedAtRaw string
	var appliedAtRaw, revokedAtRaw sql.NullString
	if err := scanner.Scan(
		&record.BindingID,
		&record.GrantID,
		&record.SurfaceID,
		&record.GrantedTo,
		&record.CapabilityKind,
		&record.TargetResource,
		&record.DesiredPolicyJSON,
		&record.AppliedPolicyHash,
		&record.ObservedPolicyHash,
		&record.Status,
		&record.DriftReason,
		&createdAtRaw,
		&updatedAtRaw,
		&appliedAtRaw,
		&revokedAtRaw,
	); err != nil {
		return TailnetGrantBinding{}, err
	}
	var err error
	record.CreatedAt, err = parseSQLiteTime(createdAtRaw)
	if err != nil {
		return TailnetGrantBinding{}, err
	}
	record.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return TailnetGrantBinding{}, err
	}
	if appliedAtRaw.Valid && strings.TrimSpace(appliedAtRaw.String) != "" {
		record.AppliedAt, err = parseSQLiteTime(appliedAtRaw.String)
		if err != nil {
			return TailnetGrantBinding{}, err
		}
	}
	if revokedAtRaw.Valid && strings.TrimSpace(revokedAtRaw.String) != "" {
		record.RevokedAt, err = parseSQLiteTime(revokedAtRaw.String)
		if err != nil {
			return TailnetGrantBinding{}, err
		}
	}
	return NormalizeTailnetGrantBinding(record), nil
}

func tailnetGrantBindingEventForUpsert(previous TailnetGrantBinding, previousOK bool, next TailnetGrantBinding) (string, string) {
	if !previousOK {
		return "created", strings.TrimSpace(next.Status)
	}
	if previous.Status != next.Status {
		return "status_changed", strings.TrimSpace(next.DriftReason)
	}
	if previous.AppliedPolicyHash != next.AppliedPolicyHash || previous.ObservedPolicyHash != next.ObservedPolicyHash {
		return "policy_hash_changed", strings.TrimSpace(next.DriftReason)
	}
	if previous.DesiredPolicyJSON != next.DesiredPolicyJSON || previous.SurfaceID != next.SurfaceID {
		return "updated", "binding contract changed"
	}
	return "", ""
}

func normalizeTailnetGrantBindingStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case TailnetGrantBindingStatusProposed:
		return TailnetGrantBindingStatusProposed
	case TailnetGrantBindingStatusApplied:
		return TailnetGrantBindingStatusApplied
	case TailnetGrantBindingStatusDrifted:
		return TailnetGrantBindingStatusDrifted
	case TailnetGrantBindingStatusRevoked:
		return TailnetGrantBindingStatusRevoked
	case TailnetGrantBindingStatusFailed:
		return TailnetGrantBindingStatusFailed
	default:
		return ""
	}
}
