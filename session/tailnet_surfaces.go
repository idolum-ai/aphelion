//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	TailnetSurfaceStatusDeclared = "declared"
	TailnetSurfaceStatusActive   = "active"
	TailnetSurfaceStatusDegraded = "degraded"
	TailnetSurfaceStatusRevoked  = "revoked"
)

type TailnetSurfaceRecord struct {
	SurfaceID      string
	OwnerKind      string
	OwnerID        string
	SurfaceKind    string
	Name           string
	Hostname       string
	TailnetName    string
	ListenAddr     string
	URL            string
	Tags           []string
	Status         string
	LastError      string
	DeclaredAt     time.Time
	ActivatedAt    time.Time
	LastObservedAt time.Time
	RevokedAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type TailnetSurfaceEvent struct {
	ID        int64
	SurfaceID string
	EventType string
	Status    string
	Detail    string
	CreatedAt time.Time
}

type TailnetSurfaceFilter struct {
	OwnerKind string
	OwnerID   string
	Status    string
	Limit     int
}

func (s *SQLiteStore) UpsertTailnetSurface(record TailnetSurfaceRecord) (TailnetSurfaceRecord, error) {
	if s == nil {
		return TailnetSurfaceRecord{}, fmt.Errorf("store is nil")
	}
	record = NormalizeTailnetSurfaceRecord(record)
	if record.SurfaceID == "" {
		return TailnetSurfaceRecord{}, fmt.Errorf("tailnet surface id is required")
	}
	if record.OwnerKind == "" {
		return TailnetSurfaceRecord{}, fmt.Errorf("tailnet surface owner_kind is required")
	}
	if record.SurfaceKind == "" {
		return TailnetSurfaceRecord{}, fmt.Errorf("tailnet surface surface_kind is required")
	}
	if record.Name == "" {
		return TailnetSurfaceRecord{}, fmt.Errorf("tailnet surface name is required")
	}
	tagsJSON, err := marshalStringSlice(record.Tags)
	if err != nil {
		return TailnetSurfaceRecord{}, fmt.Errorf("encode tailnet surface tags: %w", err)
	}
	now := time.Now().UTC()
	record.DeclaredAt = nonZeroTimeOrNow(record.DeclaredAt, now).UTC()
	record.CreatedAt = nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if record.Status == TailnetSurfaceStatusActive && record.ActivatedAt.IsZero() {
		record.ActivatedAt = record.UpdatedAt
	}
	if record.Status != TailnetSurfaceStatusRevoked {
		record.RevokedAt = time.Time{}
	}
	previous, previousOK, err := s.TailnetSurface(record.SurfaceID)
	if err != nil {
		return TailnetSurfaceRecord{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TailnetSurfaceRecord{}, fmt.Errorf("begin tailnet surface tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO tailnet_surfaces(
			surface_id, owner_kind, owner_id, surface_kind, name, hostname, tailnet_name, listen_addr, url,
			tags_json, status, last_error, declared_at, activated_at, last_observed_at, revoked_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(surface_id) DO UPDATE SET
			owner_kind = excluded.owner_kind,
			owner_id = excluded.owner_id,
			surface_kind = excluded.surface_kind,
			name = excluded.name,
			hostname = excluded.hostname,
			tailnet_name = excluded.tailnet_name,
			listen_addr = excluded.listen_addr,
			url = excluded.url,
			tags_json = excluded.tags_json,
			status = excluded.status,
			last_error = excluded.last_error,
			declared_at = tailnet_surfaces.declared_at,
			activated_at = COALESCE(tailnet_surfaces.activated_at, excluded.activated_at),
			last_observed_at = excluded.last_observed_at,
			revoked_at = excluded.revoked_at,
			updated_at = excluded.updated_at
	`, record.SurfaceID, record.OwnerKind, record.OwnerID, record.SurfaceKind, record.Name, record.Hostname, record.TailnetName, record.ListenAddr, record.URL, string(tagsJSON), record.Status, record.LastError, record.DeclaredAt.Format(time.RFC3339Nano), nullableTimeRFC3339(record.ActivatedAt), nullableTimeRFC3339(record.LastObservedAt), nullableTimeRFC3339(record.RevokedAt), record.CreatedAt.Format(time.RFC3339Nano), record.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return TailnetSurfaceRecord{}, fmt.Errorf("upsert tailnet surface: %w", err)
	}
	if eventType, detail := tailnetSurfaceEventForUpsert(previous, previousOK, record); eventType != "" {
		if _, err := tx.Exec(`
			INSERT INTO tailnet_surface_events(surface_id, event_type, status, detail, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, record.SurfaceID, eventType, record.Status, detail, record.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			return TailnetSurfaceRecord{}, fmt.Errorf("append tailnet surface event: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return TailnetSurfaceRecord{}, fmt.Errorf("commit tailnet surface tx: %w", err)
	}
	stored, ok, err := s.TailnetSurface(record.SurfaceID)
	if err != nil {
		return TailnetSurfaceRecord{}, err
	}
	if !ok {
		return TailnetSurfaceRecord{}, fmt.Errorf("tailnet surface %q not found after upsert", record.SurfaceID)
	}
	return stored, nil
}

func (s *SQLiteStore) TailnetSurface(surfaceID string) (TailnetSurfaceRecord, bool, error) {
	if s == nil {
		return TailnetSurfaceRecord{}, false, fmt.Errorf("store is nil")
	}
	surfaceID = strings.TrimSpace(surfaceID)
	if surfaceID == "" {
		return TailnetSurfaceRecord{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT surface_id, owner_kind, owner_id, surface_kind, name, hostname, tailnet_name, listen_addr, url,
			tags_json, status, last_error, declared_at, activated_at, last_observed_at, revoked_at, created_at, updated_at
		FROM tailnet_surfaces
		WHERE surface_id = ?
	`, surfaceID)
	record, err := scanTailnetSurface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TailnetSurfaceRecord{}, false, nil
	}
	if err != nil {
		return TailnetSurfaceRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) TailnetSurfaces(filter TailnetSurfaceFilter) ([]TailnetSurfaceRecord, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	filter.OwnerKind = strings.TrimSpace(filter.OwnerKind)
	filter.OwnerID = strings.TrimSpace(filter.OwnerID)
	filter.Status = normalizeTailnetSurfaceStatus(filter.Status)
	query := `
		SELECT surface_id, owner_kind, owner_id, surface_kind, name, hostname, tailnet_name, listen_addr, url,
			tags_json, status, last_error, declared_at, activated_at, last_observed_at, revoked_at, created_at, updated_at
		FROM tailnet_surfaces
	`
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 3)
	if filter.OwnerKind != "" {
		clauses = append(clauses, "owner_kind = ?")
		args = append(args, filter.OwnerKind)
	}
	if filter.OwnerID != "" {
		clauses = append(clauses, "owner_id = ?")
		args = append(args, filter.OwnerID)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, filter.Status)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += ` ORDER BY CASE status
		WHEN 'active' THEN 0
		WHEN 'degraded' THEN 1
		WHEN 'declared' THEN 2
		WHEN 'revoked' THEN 3
		ELSE 4
	END, updated_at DESC, surface_id ASC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tailnet surfaces: %w", err)
	}
	defer rows.Close()
	out := make([]TailnetSurfaceRecord, 0, filter.Limit)
	for rows.Next() {
		record, err := scanTailnetSurface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tailnet surfaces: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) RevokeTailnetSurface(surfaceID string, reason string, now time.Time) (TailnetSurfaceRecord, bool, error) {
	if s == nil {
		return TailnetSurfaceRecord{}, false, fmt.Errorf("store is nil")
	}
	surfaceID = strings.TrimSpace(surfaceID)
	if surfaceID == "" {
		return TailnetSurfaceRecord{}, false, nil
	}
	current, ok, err := s.TailnetSurface(surfaceID)
	if err != nil || !ok {
		return TailnetSurfaceRecord{}, ok, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if current.Status == TailnetSurfaceStatusRevoked {
		return current, true, nil
	}
	current.Status = TailnetSurfaceStatusRevoked
	current.LastError = strings.TrimSpace(reason)
	current.RevokedAt = now
	current.UpdatedAt = now
	stored, err := s.UpsertTailnetSurface(current)
	if err != nil {
		return TailnetSurfaceRecord{}, false, err
	}
	return stored, true, nil
}

func (s *SQLiteStore) TailnetSurfaceEvents(surfaceID string, limit int) ([]TailnetSurfaceEvent, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	surfaceID = strings.TrimSpace(surfaceID)
	if surfaceID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, surface_id, event_type, status, detail, created_at
		FROM tailnet_surface_events
		WHERE surface_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, surfaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("query tailnet surface events: %w", err)
	}
	defer rows.Close()
	out := make([]TailnetSurfaceEvent, 0, limit)
	for rows.Next() {
		var event TailnetSurfaceEvent
		var createdAtRaw string
		if err := rows.Scan(&event.ID, &event.SurfaceID, &event.EventType, &event.Status, &event.Detail, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan tailnet surface event: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, err
		}
		event.CreatedAt = createdAt
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tailnet surface events: %w", err)
	}
	return out, nil
}

func NormalizeTailnetSurfaceRecord(record TailnetSurfaceRecord) TailnetSurfaceRecord {
	record.SurfaceID = strings.TrimSpace(record.SurfaceID)
	record.OwnerKind = strings.TrimSpace(record.OwnerKind)
	record.OwnerID = strings.TrimSpace(record.OwnerID)
	record.SurfaceKind = strings.TrimSpace(record.SurfaceKind)
	record.Name = strings.TrimSpace(record.Name)
	record.Hostname = strings.TrimSpace(record.Hostname)
	record.TailnetName = strings.TrimSpace(record.TailnetName)
	record.ListenAddr = strings.TrimSpace(record.ListenAddr)
	record.URL = strings.TrimSpace(record.URL)
	record.Tags = normalizeTailnetSurfaceTags(record.Tags)
	record.Status = normalizeTailnetSurfaceStatus(record.Status)
	if record.Status == "" {
		record.Status = TailnetSurfaceStatusDeclared
	}
	record.LastError = strings.TrimSpace(record.LastError)
	return record
}

func scanTailnetSurface(scanner interface{ Scan(dest ...any) error }) (TailnetSurfaceRecord, error) {
	var record TailnetSurfaceRecord
	var tagsRaw string
	var declaredAtRaw, createdAtRaw, updatedAtRaw string
	var activatedAtRaw, lastObservedAtRaw, revokedAtRaw sql.NullString
	if err := scanner.Scan(
		&record.SurfaceID,
		&record.OwnerKind,
		&record.OwnerID,
		&record.SurfaceKind,
		&record.Name,
		&record.Hostname,
		&record.TailnetName,
		&record.ListenAddr,
		&record.URL,
		&tagsRaw,
		&record.Status,
		&record.LastError,
		&declaredAtRaw,
		&activatedAtRaw,
		&lastObservedAtRaw,
		&revokedAtRaw,
		&createdAtRaw,
		&updatedAtRaw,
	); err != nil {
		return TailnetSurfaceRecord{}, err
	}
	if strings.TrimSpace(tagsRaw) != "" {
		_ = json.Unmarshal([]byte(tagsRaw), &record.Tags)
		record.Tags = normalizeTailnetSurfaceTags(record.Tags)
	}
	var err error
	record.DeclaredAt, err = parseSQLiteTime(declaredAtRaw)
	if err != nil {
		return TailnetSurfaceRecord{}, err
	}
	if activatedAtRaw.Valid && strings.TrimSpace(activatedAtRaw.String) != "" {
		record.ActivatedAt, err = parseSQLiteTime(activatedAtRaw.String)
		if err != nil {
			return TailnetSurfaceRecord{}, err
		}
	}
	if lastObservedAtRaw.Valid && strings.TrimSpace(lastObservedAtRaw.String) != "" {
		record.LastObservedAt, err = parseSQLiteTime(lastObservedAtRaw.String)
		if err != nil {
			return TailnetSurfaceRecord{}, err
		}
	}
	if revokedAtRaw.Valid && strings.TrimSpace(revokedAtRaw.String) != "" {
		record.RevokedAt, err = parseSQLiteTime(revokedAtRaw.String)
		if err != nil {
			return TailnetSurfaceRecord{}, err
		}
	}
	record.CreatedAt, err = parseSQLiteTime(createdAtRaw)
	if err != nil {
		return TailnetSurfaceRecord{}, err
	}
	record.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return TailnetSurfaceRecord{}, err
	}
	record.Status = normalizeTailnetSurfaceStatus(record.Status)
	return record, nil
}

func tailnetSurfaceEventForUpsert(previous TailnetSurfaceRecord, previousOK bool, next TailnetSurfaceRecord) (string, string) {
	if !previousOK {
		if next.Status == TailnetSurfaceStatusActive {
			return "active", "surface declared active"
		}
		return "declared", strings.TrimSpace(next.LastError)
	}
	if previous.Status != next.Status {
		return "status_changed", strings.TrimSpace(next.LastError)
	}
	if previous.URL != next.URL || previous.ListenAddr != next.ListenAddr || previous.Hostname != next.Hostname || previous.TailnetName != next.TailnetName {
		return "updated", "surface endpoint changed"
	}
	if strings.TrimSpace(previous.LastError) != strings.TrimSpace(next.LastError) {
		return "updated", strings.TrimSpace(next.LastError)
	}
	return "", ""
}

func normalizeTailnetSurfaceStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case TailnetSurfaceStatusDeclared:
		return TailnetSurfaceStatusDeclared
	case TailnetSurfaceStatusActive:
		return TailnetSurfaceStatusActive
	case TailnetSurfaceStatusDegraded:
		return TailnetSurfaceStatusDegraded
	case TailnetSurfaceStatusRevoked:
		return TailnetSurfaceStatusRevoked
	default:
		return ""
	}
}

func normalizeTailnetSurfaceTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}
