//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) CreateOperatorAutonomyOverride(override OperatorAutonomyOverride) (OperatorAutonomyOverride, error) {
	override = NormalizeOperatorAutonomyOverride(override)
	if strings.TrimSpace(override.ScopeKind) == "" && strings.TrimSpace(override.ScopeID) == "" && override.ChatID != 0 {
		override.ScopeKind, override.ScopeID = OperatorAutoScopeForKey(SessionKey{ChatID: override.ChatID})
	}
	if override.ID == "" {
		return OperatorAutonomyOverride{}, fmt.Errorf("operator autonomy override id is required")
	}
	if override.AdminUserID <= 0 {
		return OperatorAutonomyOverride{}, fmt.Errorf("operator autonomy override admin_user_id is required")
	}
	if override.ChatID == 0 {
		return OperatorAutonomyOverride{}, fmt.Errorf("operator autonomy override chat_id is required")
	}
	if override.Mode == "" {
		return OperatorAutonomyOverride{}, fmt.Errorf("operator autonomy override mode is required")
	}
	if override.ExpiresAt.IsZero() {
		return OperatorAutonomyOverride{}, fmt.Errorf("operator autonomy override expires_at is required")
	}
	now := time.Now().UTC()
	if override.CreatedAt.IsZero() {
		override.CreatedAt = now
	}
	if override.UpdatedAt.IsZero() {
		override.UpdatedAt = now
	}
	revokedAt := sql.NullString{}
	if !override.RevokedAt.IsZero() {
		revokedAt = sql.NullString{String: override.RevokedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	if _, err := s.db.Exec(`
		INSERT INTO operator_autonomy_overrides(
			override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, override.ID, override.AdminUserID, override.ChatID, override.ScopeKind, override.ScopeID, override.Mode, override.Scope, override.Reason, override.CreatedAt.UTC().Format(time.RFC3339Nano), override.ExpiresAt.UTC().Format(time.RFC3339Nano), revokedAt, override.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return OperatorAutonomyOverride{}, fmt.Errorf("create operator autonomy override: %w", err)
	}
	stored, ok, err := s.OperatorAutonomyOverride(override.ID)
	if err != nil {
		return OperatorAutonomyOverride{}, err
	}
	if !ok {
		return OperatorAutonomyOverride{}, fmt.Errorf("operator autonomy override %q not found after insert", override.ID)
	}
	return stored, nil
}

func (s *SQLiteStore) OperatorAutonomyOverride(id string) (OperatorAutonomyOverride, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OperatorAutonomyOverride{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_autonomy_overrides
		WHERE override_id = ?
	`, id)
	override, err := scanOperatorAutonomyOverride(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorAutonomyOverride{}, false, nil
	}
	if err != nil {
		return OperatorAutonomyOverride{}, false, err
	}
	return override, true, nil
}

func (s *SQLiteStore) ActiveOperatorAutonomyOverrides(chatID int64, now time.Time) ([]OperatorAutonomyOverride, error) {
	if chatID == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.db.Query(`
		SELECT override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_autonomy_overrides
		WHERE chat_id = ?
			AND mode = 'leased'
			AND revoked_at IS NULL
			AND expires_at > ?
		ORDER BY updated_at DESC, created_at DESC, override_id DESC
	`, chatID, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query active operator autonomy overrides: %w", err)
	}
	defer rows.Close()
	out := make([]OperatorAutonomyOverride, 0)
	for rows.Next() {
		override, err := scanOperatorAutonomyOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active operator autonomy overrides: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) LatestOperatorAutonomyOverride(chatID int64, adminUserID int64) (OperatorAutonomyOverride, bool, error) {
	if chatID == 0 || adminUserID <= 0 {
		return OperatorAutonomyOverride{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_autonomy_overrides
		WHERE chat_id = ? AND admin_user_id = ?
		ORDER BY updated_at DESC, created_at DESC, override_id DESC
		LIMIT 1
	`, chatID, adminUserID)
	override, err := scanOperatorAutonomyOverride(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorAutonomyOverride{}, false, nil
	}
	if err != nil {
		return OperatorAutonomyOverride{}, false, err
	}
	return override, true, nil
}

func (s *SQLiteStore) RevokeOperatorAutonomyOverrides(chatID int64, adminUserID int64, now time.Time) ([]OperatorAutonomyOverride, error) {
	if chatID == 0 || adminUserID <= 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin revoke operator autonomy overrides: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_autonomy_overrides
		WHERE chat_id = ? AND admin_user_id = ? AND revoked_at IS NULL
		ORDER BY updated_at DESC, created_at DESC, override_id DESC
	`, chatID, adminUserID)
	if err != nil {
		return nil, fmt.Errorf("query operator autonomy overrides to revoke: %w", err)
	}
	overrides := make([]OperatorAutonomyOverride, 0)
	for rows.Next() {
		override, err := scanOperatorAutonomyOverride(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		overrides = append(overrides, override)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close operator autonomy revoke rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operator autonomy overrides to revoke: %w", err)
	}
	if len(overrides) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty operator autonomy revoke: %w", err)
		}
		return nil, nil
	}

	if _, err := tx.Exec(`
		UPDATE operator_autonomy_overrides
		SET revoked_at = ?, updated_at = ?
		WHERE chat_id = ? AND admin_user_id = ? AND revoked_at IS NULL
	`, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), chatID, adminUserID); err != nil {
		return nil, fmt.Errorf("revoke operator autonomy overrides: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit operator autonomy revoke: %w", err)
	}
	return overrides, nil
}

type operatorAutonomyOverrideScanner interface {
	Scan(dest ...any) error
}

func scanOperatorAutonomyOverride(scanner operatorAutonomyOverrideScanner) (OperatorAutonomyOverride, error) {
	var (
		override   OperatorAutonomyOverride
		createdRaw string
		expiresRaw string
		revokedRaw sql.NullString
		updatedRaw string
	)
	if err := scanner.Scan(
		&override.ID,
		&override.AdminUserID,
		&override.ChatID,
		&override.ScopeKind,
		&override.ScopeID,
		&override.Mode,
		&override.Scope,
		&override.Reason,
		&createdRaw,
		&expiresRaw,
		&revokedRaw,
		&updatedRaw,
	); err != nil {
		return OperatorAutonomyOverride{}, err
	}
	if createdRaw != "" {
		if t, err := parseSQLiteTime(createdRaw); err == nil {
			override.CreatedAt = t
		}
	}
	if expiresRaw != "" {
		if t, err := parseSQLiteTime(expiresRaw); err == nil {
			override.ExpiresAt = t
		}
	}
	if revokedRaw.Valid && strings.TrimSpace(revokedRaw.String) != "" {
		if t, err := parseSQLiteTime(revokedRaw.String); err == nil {
			override.RevokedAt = t
		}
	}
	if updatedRaw != "" {
		if t, err := parseSQLiteTime(updatedRaw); err == nil {
			override.UpdatedAt = t
		}
	}
	return NormalizeOperatorAutonomyOverride(override), nil
}

func (s *SQLiteStore) ActiveOperatorAutonomyOverridesForScope(chatID int64, scopeKind string, scopeID string, now time.Time) ([]OperatorAutonomyOverride, error) {
	if chatID == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.db.Query(`
		SELECT override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_autonomy_overrides
		WHERE chat_id = ?
			AND scope_kind = ?
			AND scope_id = ?
			AND mode = 'leased'
			AND revoked_at IS NULL
			AND expires_at > ?
		ORDER BY updated_at DESC, created_at DESC, override_id DESC
	`, chatID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query active scoped operator autonomy overrides: %w", err)
	}
	defer rows.Close()
	out := make([]OperatorAutonomyOverride, 0)
	for rows.Next() {
		override, err := scanOperatorAutonomyOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active scoped operator autonomy overrides: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) RevokeOperatorAutonomyOverridesForScope(chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) ([]OperatorAutonomyOverride, error) {
	if chatID == 0 || adminUserID <= 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin revoke scoped operator autonomy overrides: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_autonomy_overrides
		WHERE chat_id = ? AND admin_user_id = ? AND scope_kind = ? AND scope_id = ? AND revoked_at IS NULL
		ORDER BY updated_at DESC, created_at DESC, override_id DESC
	`, chatID, adminUserID, scopeKind, scopeID)
	if err != nil {
		return nil, fmt.Errorf("query scoped operator autonomy overrides to revoke: %w", err)
	}
	overrides := make([]OperatorAutonomyOverride, 0)
	for rows.Next() {
		override, err := scanOperatorAutonomyOverride(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		overrides = append(overrides, override)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close scoped operator autonomy revoke rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scoped operator autonomy overrides to revoke: %w", err)
	}
	if len(overrides) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty scoped operator autonomy revoke: %w", err)
		}
		return nil, nil
	}

	if _, err := tx.Exec(`
		UPDATE operator_autonomy_overrides
		SET revoked_at = ?, updated_at = ?
		WHERE chat_id = ? AND admin_user_id = ? AND scope_kind = ? AND scope_id = ? AND revoked_at IS NULL
	`, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), chatID, adminUserID, scopeKind, scopeID); err != nil {
		return nil, fmt.Errorf("revoke scoped operator autonomy overrides: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit scoped operator autonomy revoke: %w", err)
	}
	return overrides, nil
}
