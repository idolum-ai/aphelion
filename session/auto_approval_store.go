//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) CreateOperatorAutoApprovalLease(lease OperatorAutoApprovalLease) (OperatorAutoApprovalLease, error) {
	lease = NormalizeOperatorAutoApprovalLease(lease)
	if strings.TrimSpace(lease.ScopeKind) == "" && strings.TrimSpace(lease.ScopeID) == "" && lease.ChatID != 0 {
		lease.ScopeKind, lease.ScopeID = OperatorAutoScopeForKey(SessionKey{ChatID: lease.ChatID})
	}
	if lease.ID == "" {
		return OperatorAutoApprovalLease{}, fmt.Errorf("operator auto approval lease id is required")
	}
	if lease.AdminUserID <= 0 {
		return OperatorAutoApprovalLease{}, fmt.Errorf("operator auto approval admin_user_id is required")
	}
	if lease.ChatID == 0 {
		return OperatorAutoApprovalLease{}, fmt.Errorf("operator auto approval chat_id is required")
	}
	if lease.ExpiresAt.IsZero() {
		return OperatorAutoApprovalLease{}, fmt.Errorf("operator auto approval expires_at is required")
	}
	now := time.Now().UTC()
	if lease.CreatedAt.IsZero() {
		lease.CreatedAt = now
	}
	if lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = now
	}
	revokedAt := sql.NullString{}
	if !lease.RevokedAt.IsZero() {
		revokedAt = sql.NullString{String: lease.RevokedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	if _, err := s.db.Exec(`
		INSERT INTO operator_auto_approvals(
			lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, lease.ID, lease.AdminUserID, lease.ChatID, lease.ScopeKind, lease.ScopeID, lease.Scope, lease.Reason, lease.MaxUses, lease.UsedCount, lease.CreatedAt.UTC().Format(time.RFC3339Nano), lease.ExpiresAt.UTC().Format(time.RFC3339Nano), revokedAt, lease.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return OperatorAutoApprovalLease{}, fmt.Errorf("create operator auto approval lease: %w", err)
	}
	stored, ok, err := s.OperatorAutoApprovalLease(lease.ID)
	if err != nil {
		return OperatorAutoApprovalLease{}, err
	}
	if !ok {
		return OperatorAutoApprovalLease{}, fmt.Errorf("operator auto approval lease %q not found after insert", lease.ID)
	}
	return stored, nil
}

func (s *SQLiteStore) OperatorAutoApprovalLease(id string) (OperatorAutoApprovalLease, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OperatorAutoApprovalLease{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE lease_id = ?
	`, id)
	lease, err := scanOperatorAutoApprovalLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorAutoApprovalLease{}, false, nil
	}
	if err != nil {
		return OperatorAutoApprovalLease{}, false, err
	}
	return lease, true, nil
}

func (s *SQLiteStore) ActiveOperatorAutoApprovalLeases(chatID int64, now time.Time) ([]OperatorAutoApprovalLease, error) {
	if chatID == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.db.Query(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE chat_id = ?
			AND revoked_at IS NULL
			AND expires_at > ?
			AND (max_uses <= 0 OR used_count < max_uses)
		ORDER BY updated_at DESC, created_at DESC, lease_id DESC
	`, chatID, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query active operator auto approvals: %w", err)
	}
	defer rows.Close()
	out := make([]OperatorAutoApprovalLease, 0)
	for rows.Next() {
		lease, err := scanOperatorAutoApprovalLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active operator auto approvals: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) OperatorAutoApprovalLeases(limit int, now time.Time, activeOnly bool) ([]OperatorAutoApprovalLease, error) {
	if limit <= 0 {
		limit = 50
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := `
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
	`
	args := make([]any, 0, 2)
	if activeOnly {
		query += `
			WHERE revoked_at IS NULL
				AND expires_at > ?
				AND (max_uses <= 0 OR used_count < max_uses)
		`
		args = append(args, now.UTC().Format(time.RFC3339Nano))
	}
	query += `
		ORDER BY updated_at DESC, created_at DESC, lease_id DESC
		LIMIT ?
	`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query operator auto approvals: %w", err)
	}
	defer rows.Close()
	out := make([]OperatorAutoApprovalLease, 0, limit)
	for rows.Next() {
		lease, err := scanOperatorAutoApprovalLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operator auto approvals: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) LatestOperatorAutoApprovalLease(chatID int64, adminUserID int64) (OperatorAutoApprovalLease, bool, error) {
	if chatID == 0 || adminUserID <= 0 {
		return OperatorAutoApprovalLease{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE chat_id = ? AND admin_user_id = ?
		ORDER BY updated_at DESC, created_at DESC, lease_id DESC
		LIMIT 1
	`, chatID, adminUserID)
	lease, err := scanOperatorAutoApprovalLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorAutoApprovalLease{}, false, nil
	}
	if err != nil {
		return OperatorAutoApprovalLease{}, false, err
	}
	return lease, true, nil
}

func (s *SQLiteStore) IncrementOperatorAutoApprovalUse(id string, now time.Time) (OperatorAutoApprovalLease, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OperatorAutoApprovalLease{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	res, err := s.db.Exec(`
		UPDATE operator_auto_approvals
		SET used_count = used_count + 1, updated_at = ?
		WHERE lease_id = ?
			AND revoked_at IS NULL
			AND expires_at > ?
			AND (max_uses <= 0 OR used_count < max_uses)
	`, now.UTC().Format(time.RFC3339Nano), id, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return OperatorAutoApprovalLease{}, false, fmt.Errorf("increment operator auto approval use: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return OperatorAutoApprovalLease{}, false, fmt.Errorf("operator auto approval use rows affected: %w", err)
	}
	if affected == 0 {
		return OperatorAutoApprovalLease{}, false, nil
	}
	lease, ok, err := s.OperatorAutoApprovalLease(id)
	if err != nil {
		return OperatorAutoApprovalLease{}, false, err
	}
	return lease, ok, nil
}

func (s *SQLiteStore) RevokeOperatorAutoApprovalLeases(chatID int64, adminUserID int64, now time.Time) ([]OperatorAutoApprovalLease, error) {
	if chatID == 0 || adminUserID <= 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin revoke operator auto approval leases: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE chat_id = ? AND admin_user_id = ? AND revoked_at IS NULL
		ORDER BY updated_at DESC, created_at DESC, lease_id DESC
	`, chatID, adminUserID)
	if err != nil {
		return nil, fmt.Errorf("query operator auto approval leases to revoke: %w", err)
	}
	leases := make([]OperatorAutoApprovalLease, 0)
	for rows.Next() {
		lease, err := scanOperatorAutoApprovalLease(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		leases = append(leases, lease)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close operator auto approval revoke rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operator auto approval leases to revoke: %w", err)
	}
	if len(leases) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty operator auto approval revoke: %w", err)
		}
		return nil, nil
	}

	if _, err := tx.Exec(`
		UPDATE operator_auto_approvals
		SET revoked_at = ?, updated_at = ?
		WHERE chat_id = ? AND admin_user_id = ? AND revoked_at IS NULL
	`, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), chatID, adminUserID); err != nil {
		return nil, fmt.Errorf("revoke operator auto approval leases: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit operator auto approval revoke: %w", err)
	}
	return leases, nil
}

type operatorAutoApprovalLeaseScanner interface {
	Scan(dest ...any) error
}

func scanOperatorAutoApprovalLease(scanner operatorAutoApprovalLeaseScanner) (OperatorAutoApprovalLease, error) {
	var (
		lease      OperatorAutoApprovalLease
		createdRaw string
		expiresRaw string
		revokedRaw sql.NullString
		updatedRaw string
	)
	if err := scanner.Scan(
		&lease.ID,
		&lease.AdminUserID,
		&lease.ChatID,
		&lease.ScopeKind,
		&lease.ScopeID,
		&lease.Scope,
		&lease.Reason,
		&lease.MaxUses,
		&lease.UsedCount,
		&createdRaw,
		&expiresRaw,
		&revokedRaw,
		&updatedRaw,
	); err != nil {
		return OperatorAutoApprovalLease{}, err
	}
	if createdRaw != "" {
		if t, err := parseSQLiteTime(createdRaw); err == nil {
			lease.CreatedAt = t
		}
	}
	if expiresRaw != "" {
		if t, err := parseSQLiteTime(expiresRaw); err == nil {
			lease.ExpiresAt = t
		}
	}
	if revokedRaw.Valid && strings.TrimSpace(revokedRaw.String) != "" {
		if t, err := parseSQLiteTime(revokedRaw.String); err == nil {
			lease.RevokedAt = t
		}
	}
	if updatedRaw != "" {
		if t, err := parseSQLiteTime(updatedRaw); err == nil {
			lease.UpdatedAt = t
		}
	}
	return NormalizeOperatorAutoApprovalLease(lease), nil
}

func (s *SQLiteStore) ActiveOperatorAutoApprovalLeasesForScope(chatID int64, scopeKind string, scopeID string, now time.Time) ([]OperatorAutoApprovalLease, error) {
	if chatID == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	rows, err := s.db.Query(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE chat_id = ?
			AND scope_kind = ?
			AND scope_id = ?
			AND revoked_at IS NULL
			AND expires_at > ?
			AND (max_uses <= 0 OR used_count < max_uses)
		ORDER BY updated_at DESC, created_at DESC, lease_id DESC
	`, chatID, scopeKind, scopeID, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query active scoped operator auto approvals: %w", err)
	}
	defer rows.Close()
	out := make([]OperatorAutoApprovalLease, 0)
	for rows.Next() {
		lease, err := scanOperatorAutoApprovalLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active scoped operator auto approvals: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) LatestOperatorAutoApprovalLeaseForScope(chatID int64, adminUserID int64, scopeKind string, scopeID string) (OperatorAutoApprovalLease, bool, error) {
	if chatID == 0 || adminUserID <= 0 {
		return OperatorAutoApprovalLease{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE chat_id = ? AND admin_user_id = ? AND scope_kind = ? AND scope_id = ?
		ORDER BY updated_at DESC, created_at DESC, lease_id DESC
		LIMIT 1
	`, chatID, adminUserID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID))
	lease, err := scanOperatorAutoApprovalLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorAutoApprovalLease{}, false, nil
	}
	if err != nil {
		return OperatorAutoApprovalLease{}, false, err
	}
	return lease, true, nil
}

func (s *SQLiteStore) RevokeOperatorAutoApprovalLeasesForScope(chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) ([]OperatorAutoApprovalLease, error) {
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
		return nil, fmt.Errorf("begin revoke scoped operator auto approval leases: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE chat_id = ? AND admin_user_id = ? AND scope_kind = ? AND scope_id = ? AND revoked_at IS NULL
		ORDER BY updated_at DESC, created_at DESC, lease_id DESC
	`, chatID, adminUserID, scopeKind, scopeID)
	if err != nil {
		return nil, fmt.Errorf("query scoped operator auto approval leases to revoke: %w", err)
	}
	leases := make([]OperatorAutoApprovalLease, 0)
	for rows.Next() {
		lease, err := scanOperatorAutoApprovalLease(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		leases = append(leases, lease)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close scoped operator auto approval revoke rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scoped operator auto approval leases to revoke: %w", err)
	}
	if len(leases) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty scoped operator auto approval revoke: %w", err)
		}
		return nil, nil
	}

	if _, err := tx.Exec(`
		UPDATE operator_auto_approvals
		SET revoked_at = ?, updated_at = ?
		WHERE chat_id = ? AND admin_user_id = ? AND scope_kind = ? AND scope_id = ? AND revoked_at IS NULL
	`, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), chatID, adminUserID, scopeKind, scopeID); err != nil {
		return nil, fmt.Errorf("revoke scoped operator auto approval leases: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit scoped operator auto approval revoke: %w", err)
	}
	return leases, nil
}
