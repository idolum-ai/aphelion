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

func (s *SQLiteStore) OpenApprovalWindowOfferWithAuthority(offerID string, createdLease OperatorAutoApprovalLease, createdOverride OperatorAutonomyOverride, now time.Time) (ApprovalWindowOffer, OperatorAutoApprovalLease, OperatorAutonomyOverride, bool, error) {
	offerID = strings.TrimSpace(offerID)
	if offerID == "" {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	createdLease = NormalizeOperatorAutoApprovalLease(createdLease)
	createdOverride = NormalizeOperatorAutonomyOverride(createdOverride)
	if createdLease.ID == "" || createdOverride.ID == "" {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("approval window authority ids are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("begin approval window offer open: %w", err)
	}
	defer tx.Rollback()

	offer, ok, err := approvalWindowOfferByIDTx(tx, offerID)
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if !ok {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if err := validateApprovalWindowAuthorityMatchesOffer(offer, createdLease, createdOverride); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if err := revokeScopedOperatorAutonomyOverridesTx(tx, offer.ChatID, offer.AdminUserID, offer.ScopeKind, offer.ScopeID, now); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if err := revokeScopedOperatorAutoApprovalLeasesTx(tx, offer.ChatID, offer.AdminUserID, offer.ScopeKind, offer.ScopeID, now); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if err := insertOperatorAutonomyOverrideTx(tx, createdOverride); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if err := insertOperatorAutoApprovalLeaseTx(tx, createdLease); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if ok, err := markApprovalWindowOfferOpenedTx(tx, offer.ID, "", "", createdLease.ID, createdOverride.ID, now, true); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	} else if !ok {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	opened, ok, err := approvalWindowOfferByIDTx(tx, offer.ID)
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if !ok {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("approval window offer %q not found after open", offer.ID)
	}
	if err := tx.Commit(); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("commit approval window offer open: %w", err)
	}
	return opened, createdLease, createdOverride, true, nil
}

func (s *SQLiteStore) ReplaceApprovalWindowOfferAuthorityByIDs(offerID string, chatID int64, adminUserID int64, scopeKind string, scopeID string, leaseID string, overrideID string, createdLease OperatorAutoApprovalLease, createdOverride OperatorAutonomyOverride, now time.Time) (ApprovalWindowOffer, OperatorAutoApprovalLease, OperatorAutonomyOverride, OperatorAutoApprovalLease, OperatorAutonomyOverride, bool, error) {
	offerID = strings.TrimSpace(offerID)
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	leaseID = strings.TrimSpace(leaseID)
	overrideID = strings.TrimSpace(overrideID)
	if offerID == "" || chatID == 0 || adminUserID <= 0 || scopeKind == "" || scopeID == "" || leaseID == "" || overrideID == "" {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	createdLease = NormalizeOperatorAutoApprovalLease(createdLease)
	createdOverride = NormalizeOperatorAutonomyOverride(createdOverride)
	if createdLease.ID == "" || createdOverride.ID == "" {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("replacement approval window ids are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("begin approval window offer replace: %w", err)
	}
	defer tx.Rollback()

	offer, offerOK, err := approvalWindowOfferByIDTx(tx, offerID)
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if !offerOK || offer.ChatID != chatID || offer.AdminUserID != adminUserID || offer.ScopeKind != scopeKind || offer.ScopeID != scopeID || !offer.ActiveAt(now) {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if err := validateApprovalWindowAuthorityMatchesOffer(offer, createdLease, createdOverride); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	oldLease, leaseOK, err := activeOperatorAutoApprovalLeaseByIDTx(tx, leaseID, chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	oldOverride, overrideOK, err := activeOperatorAutonomyOverrideByIDTx(tx, overrideID, chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if !leaseOK || !overrideOK {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if ok, err := revokeOperatorAutoApprovalLeaseByIDTx(tx, leaseID, chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	} else if !ok {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if ok, err := revokeOperatorAutonomyOverrideByIDTx(tx, overrideID, chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	} else if !ok {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	if err := insertOperatorAutonomyOverrideTx(tx, createdOverride); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if err := insertOperatorAutoApprovalLeaseTx(tx, createdLease); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if ok, err := markApprovalWindowOfferOpenedTx(tx, offer.ID, leaseID, overrideID, createdLease.ID, createdOverride.ID, now, false); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	} else if !ok {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, nil
	}
	opened, ok, err := approvalWindowOfferByIDTx(tx, offer.ID)
	if err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, err
	}
	if !ok {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("approval window offer %q not found after replace", offer.ID)
	}
	if err := tx.Commit(); err != nil {
		return ApprovalWindowOffer{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, OperatorAutoApprovalLease{}, OperatorAutonomyOverride{}, false, fmt.Errorf("commit approval window offer replace: %w", err)
	}
	oldLease.RevokedAt = now
	oldLease.UpdatedAt = now
	oldOverride.RevokedAt = now
	oldOverride.UpdatedAt = now
	return opened, NormalizeOperatorAutoApprovalLease(oldLease), NormalizeOperatorAutonomyOverride(oldOverride), createdLease, createdOverride, true, nil
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

func approvalWindowOfferByIDTx(tx *sql.Tx, id string) (ApprovalWindowOffer, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ApprovalWindowOffer{}, false, nil
	}
	row := tx.QueryRow(`
		SELECT offer_id, chat_id, admin_user_id, session_id, scope_kind, scope_id, durable_agent_id,
			source_kind, source_id, source_decision_kind, opened_lease_id, opened_override_id,
			created_at, expires_at, used_at, closed_at, updated_at
		FROM approval_window_offers
		WHERE offer_id = ?
	`, id)
	offer, err := scanApprovalWindowOffer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalWindowOffer{}, false, nil
	}
	if err != nil {
		return ApprovalWindowOffer{}, false, err
	}
	return offer, true, nil
}

func validateApprovalWindowAuthorityMatchesOffer(offer ApprovalWindowOffer, lease OperatorAutoApprovalLease, override OperatorAutonomyOverride) error {
	offer = NormalizeApprovalWindowOffer(offer)
	lease = NormalizeOperatorAutoApprovalLease(lease)
	override = NormalizeOperatorAutonomyOverride(override)
	if offer.ChatID == 0 || offer.AdminUserID <= 0 || offer.ScopeKind == "" || offer.ScopeID == "" {
		return fmt.Errorf("approval window offer authority target is incomplete")
	}
	if lease.ChatID != offer.ChatID || lease.AdminUserID != offer.AdminUserID || lease.ScopeKind != offer.ScopeKind || lease.ScopeID != offer.ScopeID {
		return fmt.Errorf("approval window lease does not match offer target")
	}
	if override.ChatID != offer.ChatID || override.AdminUserID != offer.AdminUserID || override.ScopeKind != offer.ScopeKind || override.ScopeID != offer.ScopeID {
		return fmt.Errorf("approval window override does not match offer target")
	}
	if lease.ExpiresAt.IsZero() || override.ExpiresAt.IsZero() {
		return fmt.Errorf("approval window authority expiry is required")
	}
	return nil
}

func revokeScopedOperatorAutoApprovalLeasesTx(tx *sql.Tx, chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		UPDATE operator_auto_approvals
		SET revoked_at = ?, updated_at = ?
		WHERE chat_id = ? AND admin_user_id = ? AND scope_kind = ? AND scope_id = ? AND revoked_at IS NULL AND expires_at > ?
	`, stamp, stamp, chatID, adminUserID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID), stamp); err != nil {
		return fmt.Errorf("revoke scoped operator auto approval leases: %w", err)
	}
	return nil
}

func revokeScopedOperatorAutonomyOverridesTx(tx *sql.Tx, chatID int64, adminUserID int64, scopeKind string, scopeID string, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		UPDATE operator_autonomy_overrides
		SET revoked_at = ?, updated_at = ?
		WHERE chat_id = ? AND admin_user_id = ? AND scope_kind = ? AND scope_id = ? AND revoked_at IS NULL AND expires_at > ?
	`, stamp, stamp, chatID, adminUserID, strings.TrimSpace(scopeKind), strings.TrimSpace(scopeID), stamp); err != nil {
		return fmt.Errorf("revoke scoped operator autonomy overrides: %w", err)
	}
	return nil
}

func markApprovalWindowOfferOpenedTx(tx *sql.Tx, offerID string, expectedLeaseID string, expectedOverrideID string, openedLeaseID string, openedOverrideID string, now time.Time, requireUnused bool) (bool, error) {
	offerID = strings.TrimSpace(offerID)
	openedLeaseID = strings.TrimSpace(openedLeaseID)
	openedOverrideID = strings.TrimSpace(openedOverrideID)
	if offerID == "" || openedLeaseID == "" || openedOverrideID == "" {
		return false, nil
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	where := `
		WHERE offer_id = ?
			AND closed_at IS NULL
			AND expires_at > ?
	`
	args := []any{stamp, openedLeaseID, openedOverrideID, stamp, offerID, stamp}
	if requireUnused {
		where += `
			AND used_at IS NULL
			AND TRIM(opened_lease_id) = ''
			AND TRIM(opened_override_id) = ''
		`
	} else {
		where += `
			AND opened_lease_id = ?
			AND opened_override_id = ?
		`
		args = append(args, strings.TrimSpace(expectedLeaseID), strings.TrimSpace(expectedOverrideID))
	}
	res, err := tx.Exec(`
		UPDATE approval_window_offers
		SET used_at = COALESCE(used_at, ?), opened_lease_id = ?, opened_override_id = ?, updated_at = ?
	`+where, args...)
	if err != nil {
		return false, fmt.Errorf("mark approval window offer opened transactionally: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("approval window offer opened rows affected: %w", err)
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
