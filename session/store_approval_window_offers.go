//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) CreateApprovalWindowOffer(offer ApprovalWindowOffer) (ApprovalWindowOffer, error) {
	offer = NormalizeApprovalWindowOffer(offer)
	if offer.ID == "" {
		return ApprovalWindowOffer{}, fmt.Errorf("approval window offer id is required")
	}
	if offer.ChatID == 0 {
		return ApprovalWindowOffer{}, fmt.Errorf("approval window offer chat_id is required")
	}
	if offer.ScopeKind == "" || offer.ScopeID == "" {
		return ApprovalWindowOffer{}, fmt.Errorf("approval window offer scope is required")
	}
	if offer.SourceKind == "" || offer.SourceID == "" {
		return ApprovalWindowOffer{}, fmt.Errorf("approval window offer source is required")
	}
	now := time.Now().UTC()
	if offer.CreatedAt.IsZero() {
		offer.CreatedAt = now
	}
	if offer.UpdatedAt.IsZero() {
		offer.UpdatedAt = now
	}
	if offer.ExpiresAt.IsZero() {
		offer.ExpiresAt = now.Add(24 * time.Hour)
	}
	usedAt := nullableTimeString(offer.UsedAt)
	closedAt := nullableTimeString(offer.ClosedAt)
	_, err := s.db.Exec(`
		INSERT INTO approval_window_offers(
			offer_id, chat_id, admin_user_id, session_id, scope_kind, scope_id, durable_agent_id,
			source_kind, source_id, source_decision_kind, opened_lease_id, opened_override_id, created_at, expires_at, used_at, closed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, offer.ID, offer.ChatID, offer.AdminUserID, offer.SessionID, offer.ScopeKind, offer.ScopeID, offer.DurableAgentID, offer.SourceKind, offer.SourceID, offer.SourceDecisionKind, offer.OpenedLeaseID, offer.OpenedOverrideID, offer.CreatedAt.UTC().Format(time.RFC3339Nano), offer.ExpiresAt.UTC().Format(time.RFC3339Nano), usedAt, closedAt, offer.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ApprovalWindowOffer{}, fmt.Errorf("create approval window offer: %w", err)
	}
	stored, ok, err := s.ApprovalWindowOffer(offer.ID)
	if err != nil {
		return ApprovalWindowOffer{}, err
	}
	if !ok {
		return ApprovalWindowOffer{}, fmt.Errorf("approval window offer %q not found after insert", offer.ID)
	}
	return stored, nil
}

func (s *SQLiteStore) ApprovalWindowOffer(id string) (ApprovalWindowOffer, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ApprovalWindowOffer{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT offer_id, chat_id, admin_user_id, session_id, scope_kind, scope_id, durable_agent_id,
			source_kind, source_id, source_decision_kind, opened_lease_id, opened_override_id, created_at, expires_at, used_at, closed_at, updated_at
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

func (s *SQLiteStore) ActiveApprovalWindowOfferForSource(chatID int64, sourceKind string, sourceID string, now time.Time) (ApprovalWindowOffer, bool, error) {
	if chatID == 0 || strings.TrimSpace(sourceKind) == "" || strings.TrimSpace(sourceID) == "" {
		return ApprovalWindowOffer{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row := s.db.QueryRow(`
		SELECT offer_id, chat_id, admin_user_id, session_id, scope_kind, scope_id, durable_agent_id,
			source_kind, source_id, source_decision_kind, opened_lease_id, opened_override_id, created_at, expires_at, used_at, closed_at, updated_at
		FROM approval_window_offers
		WHERE chat_id = ?
			AND source_kind = ?
			AND source_id = ?
			AND (
				used_at IS NULL
				OR (
					TRIM(opened_lease_id) != ''
					AND TRIM(opened_override_id) != ''
					AND EXISTS (
						SELECT 1 FROM operator_auto_approvals leases
						WHERE leases.lease_id = approval_window_offers.opened_lease_id
							AND leases.chat_id = approval_window_offers.chat_id
							AND leases.admin_user_id = approval_window_offers.admin_user_id
							AND leases.scope_kind = approval_window_offers.scope_kind
							AND leases.scope_id = approval_window_offers.scope_id
							AND leases.revoked_at IS NULL
							AND leases.expires_at > ?
							AND (leases.max_uses <= 0 OR leases.used_count < leases.max_uses)
					)
					AND EXISTS (
						SELECT 1 FROM operator_autonomy_overrides overrides
						WHERE overrides.override_id = approval_window_offers.opened_override_id
							AND overrides.chat_id = approval_window_offers.chat_id
							AND overrides.admin_user_id = approval_window_offers.admin_user_id
							AND overrides.scope_kind = approval_window_offers.scope_kind
							AND overrides.scope_id = approval_window_offers.scope_id
							AND overrides.revoked_at IS NULL
							AND overrides.expires_at > ?
					)
				)
			)
			AND closed_at IS NULL
			AND expires_at > ?
		ORDER BY updated_at DESC, created_at DESC, offer_id DESC
		LIMIT 1
	`, chatID, normalizeEnumValue(sourceKind), strings.TrimSpace(sourceID), now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	offer, err := scanApprovalWindowOffer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalWindowOffer{}, false, nil
	}
	if err != nil {
		return ApprovalWindowOffer{}, false, err
	}
	return offer, true, nil
}

func (s *SQLiteStore) CloseUnusedApprovalWindowOffer(id string, now time.Time) (ApprovalWindowOffer, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ApprovalWindowOffer{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return s.closeApprovalWindowOfferWhere(id, stamp, `
			AND used_at IS NULL
			AND TRIM(opened_lease_id) = ''
			AND TRIM(opened_override_id) = ''
	`)
}

func (s *SQLiteStore) CloseClaimedUnopenedApprovalWindowOffer(id string, now time.Time) (ApprovalWindowOffer, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ApprovalWindowOffer{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return s.closeApprovalWindowOfferWhere(id, stamp, `
			AND used_at IS NOT NULL
			AND TRIM(opened_lease_id) = ''
			AND TRIM(opened_override_id) = ''
	`)
}

func (s *SQLiteStore) CloseApprovalWindowOfferIfOpened(id string, openedLeaseID string, openedOverrideID string, now time.Time) (ApprovalWindowOffer, bool, error) {
	id = strings.TrimSpace(id)
	openedLeaseID = strings.TrimSpace(openedLeaseID)
	openedOverrideID = strings.TrimSpace(openedOverrideID)
	if id == "" || openedLeaseID == "" || openedOverrideID == "" {
		return ApprovalWindowOffer{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`
		UPDATE approval_window_offers
		SET closed_at = COALESCE(closed_at, ?), updated_at = ?
		WHERE offer_id = ?
			AND closed_at IS NULL
			AND opened_lease_id = ?
			AND opened_override_id = ?
	`, stamp, stamp, id, openedLeaseID, openedOverrideID)
	if err != nil {
		return ApprovalWindowOffer{}, false, fmt.Errorf("close approval window offer if opened: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ApprovalWindowOffer{}, false, fmt.Errorf("approval window offer conditional close rows affected: %w", err)
	}
	if affected == 0 {
		return ApprovalWindowOffer{}, false, nil
	}
	offer, ok, err := s.ApprovalWindowOffer(id)
	if err != nil {
		return ApprovalWindowOffer{}, false, err
	}
	return offer, ok, nil
}

func (s *SQLiteStore) closeApprovalWindowOfferWhere(id string, stamp string, extraWhere string) (ApprovalWindowOffer, bool, error) {
	res, err := s.db.Exec(`
		UPDATE approval_window_offers
		SET closed_at = COALESCE(closed_at, ?), updated_at = ?
		WHERE offer_id = ?
			AND closed_at IS NULL
	`+extraWhere, stamp, stamp, id)
	if err != nil {
		return ApprovalWindowOffer{}, false, fmt.Errorf("close approval window offer conditionally: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ApprovalWindowOffer{}, false, fmt.Errorf("approval window offer conditional close rows affected: %w", err)
	}
	if affected == 0 {
		return ApprovalWindowOffer{}, false, nil
	}
	offer, ok, err := s.ApprovalWindowOffer(id)
	if err != nil {
		return ApprovalWindowOffer{}, false, err
	}
	return offer, ok, nil
}

func (s *SQLiteStore) CloseApprovalWindowOffer(id string, now time.Time) (ApprovalWindowOffer, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ApprovalWindowOffer{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`
		UPDATE approval_window_offers
		SET closed_at = COALESCE(closed_at, ?), updated_at = ?
		WHERE offer_id = ?
	`, stamp, stamp, id)
	if err != nil {
		return ApprovalWindowOffer{}, false, fmt.Errorf("close approval window offer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ApprovalWindowOffer{}, false, fmt.Errorf("approval window offer close rows affected: %w", err)
	}
	if affected == 0 {
		return ApprovalWindowOffer{}, false, nil
	}
	offer, ok, err := s.ApprovalWindowOffer(id)
	if err != nil {
		return ApprovalWindowOffer{}, false, err
	}
	return offer, ok, nil
}

type approvalWindowOfferScanner interface {
	Scan(dest ...any) error
}

func scanApprovalWindowOffer(scanner approvalWindowOfferScanner) (ApprovalWindowOffer, error) {
	var (
		offer      ApprovalWindowOffer
		createdRaw string
		expiresRaw string
		usedRaw    sql.NullString
		closedRaw  sql.NullString
		updatedRaw string
	)
	if err := scanner.Scan(&offer.ID, &offer.ChatID, &offer.AdminUserID, &offer.SessionID, &offer.ScopeKind, &offer.ScopeID, &offer.DurableAgentID, &offer.SourceKind, &offer.SourceID, &offer.SourceDecisionKind, &offer.OpenedLeaseID, &offer.OpenedOverrideID, &createdRaw, &expiresRaw, &usedRaw, &closedRaw, &updatedRaw); err != nil {
		return ApprovalWindowOffer{}, err
	}
	var err error
	if offer.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return ApprovalWindowOffer{}, fmt.Errorf("parse approval window offer created_at: %w", err)
	}
	if offer.ExpiresAt, err = parseSQLiteTime(expiresRaw); err != nil {
		return ApprovalWindowOffer{}, fmt.Errorf("parse approval window offer expires_at: %w", err)
	}
	if usedRaw.Valid && strings.TrimSpace(usedRaw.String) != "" {
		if offer.UsedAt, err = parseSQLiteTime(usedRaw.String); err != nil {
			return ApprovalWindowOffer{}, fmt.Errorf("parse approval window offer used_at: %w", err)
		}
	}
	if closedRaw.Valid && strings.TrimSpace(closedRaw.String) != "" {
		if offer.ClosedAt, err = parseSQLiteTime(closedRaw.String); err != nil {
			return ApprovalWindowOffer{}, fmt.Errorf("parse approval window offer closed_at: %w", err)
		}
	}
	if offer.UpdatedAt, err = parseSQLiteTime(updatedRaw); err != nil {
		return ApprovalWindowOffer{}, fmt.Errorf("parse approval window offer updated_at: %w", err)
	}
	return NormalizeApprovalWindowOffer(offer), nil
}

func nullableTimeString(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339Nano), Valid: true}
}
