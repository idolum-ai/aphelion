//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) RevokeOperatorApprovalWindowByIDs(chatID int64, adminUserID int64, scopeKind string, scopeID string, leaseID string, overrideID string, now time.Time) ([]OperatorAutoApprovalLease, []OperatorAutonomyOverride, bool, error) {
	if chatID == 0 || adminUserID <= 0 {
		return nil, nil, false, nil
	}
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	leaseID = strings.TrimSpace(leaseID)
	overrideID = strings.TrimSpace(overrideID)
	if scopeKind == "" || scopeID == "" || leaseID == "" || overrideID == "" {
		return nil, nil, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, false, fmt.Errorf("begin exact approval window revoke: %w", err)
	}
	defer tx.Rollback()

	lease, leaseOK, err := activeOperatorAutoApprovalLeaseByIDTx(tx, leaseID, chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return nil, nil, false, err
	}
	override, overrideOK, err := activeOperatorAutonomyOverrideByIDTx(tx, overrideID, chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return nil, nil, false, err
	}
	if !leaseOK || !overrideOK {
		return nil, nil, false, nil
	}
	if ok, err := revokeOperatorAutoApprovalLeaseByIDTx(tx, leaseID, chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return nil, nil, false, err
	} else if !ok {
		return nil, nil, false, nil
	}
	if ok, err := revokeOperatorAutonomyOverrideByIDTx(tx, overrideID, chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return nil, nil, false, err
	} else if !ok {
		return nil, nil, false, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, false, fmt.Errorf("commit exact approval window revoke: %w", err)
	}
	lease.RevokedAt = now
	lease.UpdatedAt = now
	override.RevokedAt = now
	override.UpdatedAt = now
	return []OperatorAutoApprovalLease{NormalizeOperatorAutoApprovalLease(lease)}, []OperatorAutonomyOverride{NormalizeOperatorAutonomyOverride(override)}, true, nil
}

func (s *SQLiteStore) ReplaceOperatorApprovalWindowByIDs(chatID int64, adminUserID int64, scopeKind string, scopeID string, leaseID string, overrideID string, createdLease OperatorAutoApprovalLease, createdOverride OperatorAutonomyOverride, now time.Time) (OperatorAutoApprovalLease, OperatorAutonomyOverride, OperatorAutoApprovalLease, OperatorAutonomyOverride, bool, error) {
	if chatID == 0 || adminUserID <= 0 {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	leaseID = strings.TrimSpace(leaseID)
	overrideID = strings.TrimSpace(overrideID)
	if scopeKind == "" || scopeID == "" || leaseID == "" || overrideID == "" {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	createdLease = NormalizeOperatorAutoApprovalLease(createdLease)
	createdOverride = NormalizeOperatorAutonomyOverride(createdOverride)
	if createdLease.ID == "" || createdOverride.ID == "" {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("replacement approval window ids are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("begin exact approval window replace: %w", err)
	}
	defer tx.Rollback()

	oldLease, leaseOK, err := activeOperatorAutoApprovalLeaseByIDTx(tx, leaseID, chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	oldOverride, overrideOK, err := activeOperatorAutonomyOverrideByIDTx(tx, overrideID, chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if !leaseOK || !overrideOK {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if ok, err := revokeOperatorAutoApprovalLeaseByIDTx(tx, leaseID, chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	} else if !ok {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if ok, err := revokeOperatorAutonomyOverrideByIDTx(tx, overrideID, chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	} else if !ok {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if err := insertOperatorAutonomyOverrideTx(tx, createdOverride); err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if err := insertOperatorAutoApprovalLeaseTx(tx, createdLease); err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("commit exact approval window replace: %w", err)
	}
	oldLease.RevokedAt = now
	oldLease.UpdatedAt = now
	oldOverride.RevokedAt = now
	oldOverride.UpdatedAt = now
	return NormalizeOperatorAutoApprovalLease(oldLease), NormalizeOperatorAutonomyOverride(oldOverride), createdLease, createdOverride, true, nil
}

func activeOperatorAutoApprovalLeaseByIDTx(tx *sql.Tx, leaseID string, chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) (OperatorAutoApprovalLease, bool, error) {
	row := tx.QueryRow(`
		SELECT lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_auto_approvals
		WHERE lease_id = ?
			AND chat_id = ?
			AND admin_user_id = ?
			AND scope_kind = ?
			AND scope_id = ?
			AND revoked_at IS NULL
			AND expires_at > ?
			AND (max_uses <= 0 OR used_count < max_uses)
	`, strings.TrimSpace(leaseID), chatID, adminUserID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID), now.UTC().Format(time.RFC3339Nano))
	lease, err := scanOperatorAutoApprovalLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorAutoApprovalLease{}, false, nil
	}
	if err != nil {
		return OperatorAutoApprovalLease{}, false, fmt.Errorf("query exact operator auto approval lease: %w", err)
	}
	return lease, true, nil
}

func activeOperatorAutonomyOverrideByIDTx(tx *sql.Tx, overrideID string, chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) (OperatorAutonomyOverride, bool, error) {
	row := tx.QueryRow(`
		SELECT override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		FROM operator_autonomy_overrides
		WHERE override_id = ?
			AND chat_id = ?
			AND admin_user_id = ?
			AND scope_kind = ?
			AND scope_id = ?
			AND revoked_at IS NULL
			AND expires_at > ?
	`, strings.TrimSpace(overrideID), chatID, adminUserID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID), now.UTC().Format(time.RFC3339Nano))
	override, err := scanOperatorAutonomyOverride(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OperatorAutonomyOverride{}, false, nil
	}
	if err != nil {
		return OperatorAutonomyOverride{}, false, fmt.Errorf("query exact operator autonomy override: %w", err)
	}
	return override, true, nil
}

func revokeOperatorAutoApprovalLeaseByIDTx(tx *sql.Tx, leaseID string, chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) (bool, error) {
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`
		UPDATE operator_auto_approvals
		SET revoked_at = ?, updated_at = ?
		WHERE lease_id = ?
			AND chat_id = ?
			AND admin_user_id = ?
			AND scope_kind = ?
			AND scope_id = ?
			AND revoked_at IS NULL
			AND expires_at > ?
			AND (max_uses <= 0 OR used_count < max_uses)
	`, stamp, stamp, strings.TrimSpace(leaseID), chatID, adminUserID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID), stamp)
	if err != nil {
		return false, fmt.Errorf("revoke exact operator auto approval lease: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("exact operator auto approval lease rows affected: %w", err)
	}
	return count == 1, nil
}

func revokeOperatorAutonomyOverrideByIDTx(tx *sql.Tx, overrideID string, chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) (bool, error) {
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`
		UPDATE operator_autonomy_overrides
		SET revoked_at = ?, updated_at = ?
		WHERE override_id = ?
			AND chat_id = ?
			AND admin_user_id = ?
			AND scope_kind = ?
			AND scope_id = ?
			AND revoked_at IS NULL
			AND expires_at > ?
	`, stamp, stamp, strings.TrimSpace(overrideID), chatID, adminUserID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID), stamp)
	if err != nil {
		return false, fmt.Errorf("revoke exact operator autonomy override: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("exact operator autonomy override rows affected: %w", err)
	}
	return count == 1, nil
}

func insertOperatorAutoApprovalLeaseTx(tx *sql.Tx, lease OperatorAutoApprovalLease) error {
	lease = NormalizeOperatorAutoApprovalLease(lease)
	revokedAt := sql.NullString{}
	if !lease.RevokedAt.IsZero() {
		revokedAt = sql.NullString{String: lease.RevokedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	if _, err := tx.Exec(`
		INSERT INTO operator_auto_approvals(
			lease_id, admin_user_id, chat_id, scope_kind, scope_id, scope, reason, max_uses, used_count,
			created_at, expires_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, lease.ID, lease.AdminUserID, lease.ChatID, lease.ScopeKind, lease.ScopeID, lease.Scope, lease.Reason, lease.MaxUses, lease.UsedCount, lease.CreatedAt.UTC().Format(time.RFC3339Nano), lease.ExpiresAt.UTC().Format(time.RFC3339Nano), revokedAt, lease.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert replacement operator auto approval lease: %w", err)
	}
	return nil
}

func insertOperatorAutonomyOverrideTx(tx *sql.Tx, override OperatorAutonomyOverride) error {
	override = NormalizeOperatorAutonomyOverride(override)
	revokedAt := sql.NullString{}
	if !override.RevokedAt.IsZero() {
		revokedAt = sql.NullString{String: override.RevokedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	if _, err := tx.Exec(`
		INSERT INTO operator_autonomy_overrides(
			override_id, admin_user_id, chat_id, scope_kind, scope_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, override.ID, override.AdminUserID, override.ChatID, override.ScopeKind, override.ScopeID, override.Mode, override.Scope, override.Reason, override.CreatedAt.UTC().Format(time.RFC3339Nano), override.ExpiresAt.UTC().Format(time.RFC3339Nano), revokedAt, override.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert replacement operator autonomy override: %w", err)
	}
	return nil
}
