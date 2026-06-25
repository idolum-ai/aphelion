//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertCapabilityRequest(request CapabilityRequest) (CapabilityRequest, error) {
	request = NormalizeCapabilityRequest(request)
	if request.RequestID == "" {
		return CapabilityRequest{}, fmt.Errorf("capability request id is required")
	}
	if request.RequestedBy == "" {
		return CapabilityRequest{}, fmt.Errorf("capability request requested_by is required")
	}
	if request.Kind == "" {
		return CapabilityRequest{}, fmt.Errorf("capability request kind is required")
	}
	if request.Purpose == "" {
		return CapabilityRequest{}, fmt.Errorf("capability request purpose is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(request.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(request.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO capability_requests(
			request_id, requested_by, requested_for, parent_principal, admin_principal, kind, target_resource,
			purpose, risk_class, contract_json, constraints_json, review_status, grant_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
			requested_by = excluded.requested_by,
			requested_for = excluded.requested_for,
			parent_principal = excluded.parent_principal,
			admin_principal = excluded.admin_principal,
			kind = excluded.kind,
			target_resource = excluded.target_resource,
			purpose = excluded.purpose,
			risk_class = excluded.risk_class,
			contract_json = excluded.contract_json,
			constraints_json = excluded.constraints_json,
			review_status = excluded.review_status,
			grant_id = excluded.grant_id,
			updated_at = excluded.updated_at
	`,
		request.RequestID,
		request.RequestedBy,
		request.RequestedFor,
		request.ParentPrincipal,
		request.AdminPrincipal,
		string(request.Kind),
		request.TargetResource,
		request.Purpose,
		request.RiskClass,
		request.Contract,
		request.Constraints,
		string(request.ReviewStatus),
		request.GrantID,
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return CapabilityRequest{}, fmt.Errorf("upsert capability request: %w", err)
	}
	stored, ok, err := s.CapabilityRequest(request.RequestID)
	if err != nil {
		return CapabilityRequest{}, err
	}
	if !ok {
		return CapabilityRequest{}, fmt.Errorf("capability request %q not found after upsert", request.RequestID)
	}
	return stored, nil
}

func (s *SQLiteStore) CapabilityRequest(requestID string) (CapabilityRequest, bool, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return CapabilityRequest{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT request_id, requested_by, requested_for, parent_principal, admin_principal, kind, target_resource,
			purpose, risk_class, contract_json, constraints_json, review_status, grant_id, created_at, updated_at
		FROM capability_requests
		WHERE request_id = ?
	`, requestID)
	request, err := scanCapabilityRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CapabilityRequest{}, false, nil
	}
	if err != nil {
		return CapabilityRequest{}, false, err
	}
	return request, true, nil
}

func (s *SQLiteStore) CapabilityRequests(limit int, reviewStatus CapabilityReviewStatus, kind CapabilityKind, principal string) ([]CapabilityRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	reviewStatus = NormalizeCapabilityReviewStatus(reviewStatus)
	kind = NormalizeCapabilityKind(kind)
	principal = strings.TrimSpace(principal)
	query := `
		SELECT request_id, requested_by, requested_for, parent_principal, admin_principal, kind, target_resource,
			purpose, risk_class, contract_json, constraints_json, review_status, grant_id, created_at, updated_at
		FROM capability_requests
	`
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 3)
	if reviewStatus != "" {
		clauses = append(clauses, "review_status = ?")
		args = append(args, string(reviewStatus))
	}
	if kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, string(kind))
	}
	if principal != "" {
		clauses = append(clauses, "(requested_by = ? OR requested_for = ? OR parent_principal = ? OR admin_principal = ?)")
		args = append(args, principal, principal, principal, principal)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY updated_at DESC, request_id ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query capability requests: %w", err)
	}
	defer rows.Close()
	out := make([]CapabilityRequest, 0, limit)
	for rows.Next() {
		request, err := scanCapabilityRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capability requests: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) AppendCapabilityReview(review CapabilityReview) (CapabilityReview, error) {
	review = NormalizeCapabilityReview(review)
	if review.ReviewID == "" {
		return CapabilityReview{}, fmt.Errorf("capability review id is required")
	}
	if review.RequestID == "" {
		return CapabilityReview{}, fmt.Errorf("capability review request_id is required")
	}
	if review.Reviewer == "" {
		return CapabilityReview{}, fmt.Errorf("capability review reviewer is required")
	}
	if review.Status == "" {
		return CapabilityReview{}, fmt.Errorf("capability review status is required")
	}
	createdAt := nonZeroTimeOrNow(review.CreatedAt, time.Now().UTC()).UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return CapabilityReview{}, fmt.Errorf("begin capability review tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO capability_reviews(review_id, request_id, reviewer, reviewer_role, review_status, rationale, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, review.ReviewID, review.RequestID, review.Reviewer, review.ReviewerRole, string(review.Status), review.Rationale, createdAt.Format(time.RFC3339Nano)); err != nil {
		return CapabilityReview{}, fmt.Errorf("append capability review: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE capability_requests
		SET review_status = ?, updated_at = ?
		WHERE request_id = ?
	`, string(review.Status), createdAt.Format(time.RFC3339Nano), review.RequestID); err != nil {
		return CapabilityReview{}, fmt.Errorf("update capability request review status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CapabilityReview{}, fmt.Errorf("commit capability review tx: %w", err)
	}
	if agreementStatus := DurableChildAgreementStatusFromCapabilityReview(review.Status); agreementStatus != "" {
		if err := s.UpdateDurableChildAgreementStatusForRequest(review.RequestID, agreementStatus); err != nil {
			return CapabilityReview{}, err
		}
	}
	return CapabilityReview(review), nil
}

func (s *SQLiteStore) CapabilityReviews(requestID string, limit int) ([]CapabilityReview, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT review_id, request_id, reviewer, reviewer_role, review_status, rationale, created_at
		FROM capability_reviews
		WHERE request_id = ?
		ORDER BY created_at DESC, review_id ASC
		LIMIT ?
	`, requestID, limit)
	if err != nil {
		return nil, fmt.Errorf("query capability reviews: %w", err)
	}
	defer rows.Close()
	out := make([]CapabilityReview, 0, limit)
	for rows.Next() {
		review, err := scanCapabilityReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, review)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capability reviews: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) UpsertCapabilityGrant(grant CapabilityGrant) (CapabilityGrant, error) {
	grant = NormalizeCapabilityGrant(grant)
	if grant.GrantID == "" {
		return CapabilityGrant{}, fmt.Errorf("capability grant id is required")
	}
	if grant.GrantedTo == "" {
		return CapabilityGrant{}, fmt.Errorf("capability grant granted_to is required")
	}
	if grant.Kind == "" {
		return CapabilityGrant{}, fmt.Errorf("capability grant kind is required")
	}
	if grant.TargetResource == "" {
		return CapabilityGrant{}, fmt.Errorf("capability grant target_resource is required")
	}
	actionsJSON, err := marshalStringSlice(grant.AllowedActions)
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("encode capability grant actions: %w", err)
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(grant.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(grant.UpdatedAt, now).UTC()
	if grant.Status == CapabilityGrantStatusActive && grant.GrantedAt.IsZero() {
		grant.GrantedAt = updatedAt
	}
	if grant.Status == CapabilityGrantStatusRevoked && grant.RevokedAt.IsZero() {
		grant.RevokedAt = updatedAt
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("begin capability grant tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO capability_grants(
			grant_id, request_id, granted_by, granted_to, kind, target_resource, allowed_actions_json,
			contract_json, constraints_json, status, baseline_policy_hash, current_policy_hash, anchor_fingerprint,
			drift_source, stale_reason, invocation_count, failure_count, created_at, updated_at, granted_at,
			expires_at, revoked_at, last_invoked_at, last_failure_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(grant_id) DO UPDATE SET
			request_id = excluded.request_id,
			granted_by = excluded.granted_by,
			granted_to = excluded.granted_to,
			kind = excluded.kind,
			target_resource = excluded.target_resource,
			allowed_actions_json = excluded.allowed_actions_json,
			contract_json = excluded.contract_json,
			constraints_json = excluded.constraints_json,
			status = excluded.status,
			baseline_policy_hash = excluded.baseline_policy_hash,
			current_policy_hash = excluded.current_policy_hash,
			anchor_fingerprint = excluded.anchor_fingerprint,
			drift_source = excluded.drift_source,
			stale_reason = excluded.stale_reason,
			invocation_count = excluded.invocation_count,
			failure_count = excluded.failure_count,
			updated_at = excluded.updated_at,
			granted_at = excluded.granted_at,
			expires_at = excluded.expires_at,
			revoked_at = excluded.revoked_at,
			last_invoked_at = excluded.last_invoked_at,
			last_failure_at = excluded.last_failure_at
	`,
		grant.GrantID,
		grant.RequestID,
		grant.GrantedBy,
		grant.GrantedTo,
		string(grant.Kind),
		grant.TargetResource,
		string(actionsJSON),
		grant.Contract,
		grant.Constraints,
		string(grant.Status),
		grant.BaselinePolicyHash,
		grant.CurrentPolicyHash,
		grant.AnchorFingerprint,
		string(grant.DriftSource),
		grant.StaleReason,
		grant.InvocationCount,
		grant.FailureCount,
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
		nullableTimeRFC3339(grant.GrantedAt),
		nullableTimeRFC3339(grant.ExpiresAt),
		nullableTimeRFC3339(grant.RevokedAt),
		nullableTimeRFC3339(grant.LastInvokedAt),
		nullableTimeRFC3339(grant.LastFailureAt),
	); err != nil {
		return CapabilityGrant{}, fmt.Errorf("upsert capability grant: %w", err)
	}
	if grant.RequestID != "" {
		if _, err := tx.Exec(`
			UPDATE capability_requests
			SET grant_id = ?, updated_at = ?
			WHERE request_id = ?
		`, grant.GrantID, updatedAt.Format(time.RFC3339Nano), grant.RequestID); err != nil {
			return CapabilityGrant{}, fmt.Errorf("link capability grant to request: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CapabilityGrant{}, fmt.Errorf("commit capability grant tx: %w", err)
	}
	stored, ok, err := s.CapabilityGrant(grant.GrantID)
	if err != nil {
		return CapabilityGrant{}, err
	}
	if !ok {
		return CapabilityGrant{}, fmt.Errorf("capability grant %q not found after upsert", grant.GrantID)
	}
	return stored, nil
}

func (s *SQLiteStore) CapabilityGrant(grantID string) (CapabilityGrant, bool, error) {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return CapabilityGrant{}, false, nil
	}
	row := s.db.QueryRow(capabilityGrantSelectSQL()+` WHERE grant_id = ?`, grantID)
	grant, err := scanCapabilityGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CapabilityGrant{}, false, nil
	}
	if err != nil {
		return CapabilityGrant{}, false, err
	}
	return grant, true, nil
}

func (s *SQLiteStore) CapabilityGrants(limit int, status CapabilityGrantStatus, kind CapabilityKind, principal string) ([]CapabilityGrant, error) {
	if limit <= 0 {
		limit = 50
	}
	status = NormalizeCapabilityGrantStatus(status)
	kind = NormalizeCapabilityKind(kind)
	principal = strings.TrimSpace(principal)
	query := capabilityGrantSelectSQL()
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 3)
	if status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(status))
	}
	if kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, string(kind))
	}
	if principal != "" {
		clauses = append(clauses, "(granted_to = ? OR granted_by = ?)")
		args = append(args, principal, principal)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY updated_at DESC, grant_id ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query capability grants: %w", err)
	}
	defer rows.Close()
	out := make([]CapabilityGrant, 0, limit)
	for rows.Next() {
		grant, err := scanCapabilityGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capability grants: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) ActiveCapabilityGrant(kind CapabilityKind, targetResource string, principal string, action string) (CapabilityGrant, bool, error) {
	grants, err := s.ActiveCapabilityGrants(kind, targetResource, principal, action)
	if err != nil {
		return CapabilityGrant{}, false, err
	}
	if len(grants) == 0 {
		return CapabilityGrant{}, false, nil
	}
	return grants[0], true, nil
}

func (s *SQLiteStore) ActiveCapabilityGrants(kind CapabilityKind, targetResource string, principal string, action string) ([]CapabilityGrant, error) {
	kind = NormalizeCapabilityKind(kind)
	targetResource = strings.TrimSpace(targetResource)
	principal = strings.TrimSpace(principal)
	action = normalizeEnumValue(action)
	if kind == "" || targetResource == "" || principal == "" || action == "" {
		return nil, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.Query(capabilityGrantSelectSQL()+`
		WHERE kind = ?
			AND target_resource = ?
			AND granted_to = ?
			AND status = ?
			AND revoked_at IS NULL
			AND (expires_at IS NULL OR expires_at = '' OR expires_at > ?)
		ORDER BY updated_at DESC, grant_id ASC
	`, string(kind), targetResource, principal, string(CapabilityGrantStatusActive), now)
	if err != nil {
		return nil, fmt.Errorf("query active capability grants: %w", err)
	}
	defer rows.Close()
	out := []CapabilityGrant{}
	for rows.Next() {
		grant, err := scanCapabilityGrant(rows)
		if err != nil {
			return nil, err
		}
		if capabilityGrantAllowsAction(grant, action) {
			out = append(out, grant)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active capability grants: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) RecordCapabilityInvocation(invocation CapabilityInvocation) (CapabilityInvocation, error) {
	invocation = NormalizeCapabilityInvocation(invocation)
	if invocation.GrantID == "" {
		return CapabilityInvocation{}, fmt.Errorf("capability invocation grant_id is required")
	}
	if invocation.Action == "" {
		return CapabilityInvocation{}, fmt.Errorf("capability invocation action is required")
	}
	if invocation.Status == "" {
		invocation.Status = "succeeded"
	}
	if invocation.OutcomeStatus == "" {
		if invocation.Status == "allowed" {
			invocation.OutcomeStatus = "pending"
		} else {
			invocation.OutcomeStatus = invocation.Status
		}
	}
	createdAt := nonZeroTimeOrNow(invocation.CreatedAt, time.Now().UTC()).UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return CapabilityInvocation{}, fmt.Errorf("begin capability invocation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`
		INSERT INTO capability_invocations(
			grant_id, principal, action, status, error_text, outcome_status, outcome_error_text,
			session_id, turn_run_id, continuation_lease_id, operation_plan_lease_id, authority_source,
			created_at, completed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		invocation.GrantID,
		invocation.Principal,
		invocation.Action,
		invocation.Status,
		invocation.ErrorText,
		invocation.OutcomeStatus,
		invocation.OutcomeErrorText,
		invocation.SessionID,
		invocation.TurnRunID,
		invocation.ContinuationLeaseID,
		invocation.OperationPlanLeaseID,
		invocation.AuthoritySource,
		createdAt.Format(time.RFC3339Nano),
		nullableTimeRFC3339(invocation.CompletedAt),
	)
	if err != nil {
		return CapabilityInvocation{}, fmt.Errorf("record capability invocation: %w", err)
	}
	failureIncrement := 0
	lastFailureAt := any(nil)
	if invocation.Status == "failed" || invocation.Status == "blocked" || invocation.OutcomeStatus == "failed" {
		failureIncrement = 1
		lastFailureAt = createdAt.Format(time.RFC3339Nano)
	}
	if _, err := tx.Exec(`
		UPDATE capability_grants
		SET invocation_count = invocation_count + 1,
			failure_count = failure_count + ?,
			last_invoked_at = ?,
			last_failure_at = COALESCE(?, last_failure_at),
			updated_at = ?
		WHERE grant_id = ?
	`, failureIncrement, createdAt.Format(time.RFC3339Nano), lastFailureAt, createdAt.Format(time.RFC3339Nano), invocation.GrantID); err != nil {
		return CapabilityInvocation{}, fmt.Errorf("update capability grant invocation counters: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CapabilityInvocation{}, fmt.Errorf("commit capability invocation tx: %w", err)
	}
	id, _ := res.LastInsertId()
	invocation.InvocationID = id
	invocation.CreatedAt = createdAt
	return invocation, nil
}

func (s *SQLiteStore) CompleteCapabilityInvocation(invocationID int64, outcomeStatus string, outcomeErrorText string, completedAt time.Time) (CapabilityInvocation, error) {
	if invocationID <= 0 {
		return CapabilityInvocation{}, fmt.Errorf("capability invocation id is required")
	}
	outcomeStatus = normalizeEnumValue(outcomeStatus)
	if outcomeStatus == "" {
		outcomeStatus = "succeeded"
	}
	outcomeErrorText = strings.TrimSpace(outcomeErrorText)
	completedAt = nonZeroTimeOrNow(completedAt, time.Now().UTC()).UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return CapabilityInvocation{}, fmt.Errorf("begin capability invocation completion tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var (
		grantID      string
		priorOutcome string
	)
	if err := tx.QueryRow(`
		SELECT grant_id, outcome_status
		FROM capability_invocations
		WHERE id = ?
	`, invocationID).Scan(&grantID, &priorOutcome); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CapabilityInvocation{}, fmt.Errorf("capability invocation %d not found", invocationID)
		}
		return CapabilityInvocation{}, fmt.Errorf("load capability invocation completion target: %w", err)
	}
	if priorOutcome != "" && priorOutcome != "pending" {
		return CapabilityInvocation{}, fmt.Errorf("capability invocation %d already has outcome %q", invocationID, priorOutcome)
	}
	if _, err := tx.Exec(`
		UPDATE capability_invocations
		SET outcome_status = ?, outcome_error_text = ?, completed_at = ?
		WHERE id = ?
	`, outcomeStatus, outcomeErrorText, completedAt.Format(time.RFC3339Nano), invocationID); err != nil {
		return CapabilityInvocation{}, fmt.Errorf("complete capability invocation: %w", err)
	}
	failureIncrement := 0
	lastFailureAt := any(nil)
	if outcomeStatus == "failed" {
		failureIncrement = 1
		lastFailureAt = completedAt.Format(time.RFC3339Nano)
	}
	if _, err := tx.Exec(`
		UPDATE capability_grants
		SET failure_count = failure_count + ?,
			last_failure_at = COALESCE(?, last_failure_at),
			updated_at = ?
		WHERE grant_id = ?
	`, failureIncrement, lastFailureAt, completedAt.Format(time.RFC3339Nano), grantID); err != nil {
		return CapabilityInvocation{}, fmt.Errorf("update capability grant invocation outcome counters: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CapabilityInvocation{}, fmt.Errorf("commit capability invocation completion tx: %w", err)
	}
	return s.CapabilityInvocation(invocationID)
}

func (s *SQLiteStore) CapabilityInvocation(invocationID int64) (CapabilityInvocation, error) {
	if invocationID <= 0 {
		return CapabilityInvocation{}, fmt.Errorf("capability invocation id is required")
	}
	row := s.db.QueryRow(`
		SELECT id, grant_id, principal, action, status, error_text, outcome_status, outcome_error_text,
			session_id, turn_run_id, continuation_lease_id, operation_plan_lease_id, authority_source,
			created_at, completed_at
		FROM capability_invocations
		WHERE id = ?
	`, invocationID)
	invocation, err := scanCapabilityInvocation(row)
	if err != nil {
		return CapabilityInvocation{}, err
	}
	return invocation, nil
}

func (s *SQLiteStore) CapabilityInvocations(limit int) ([]CapabilityInvocation, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.Query(`
		SELECT id, grant_id, principal, action, status, error_text, outcome_status, outcome_error_text,
			session_id, turn_run_id, continuation_lease_id, operation_plan_lease_id, authority_source,
			created_at, completed_at
		FROM capability_invocations
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query capability invocations: %w", err)
	}
	defer rows.Close()
	return scanCapabilityInvocationRows(rows)
}

func (s *SQLiteStore) CapabilityInvocationsByGrant(grantID string, limit int) ([]CapabilityInvocation, error) {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return nil, fmt.Errorf("capability invocation grant_id is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, grant_id, principal, action, status, error_text, outcome_status, outcome_error_text,
			session_id, turn_run_id, continuation_lease_id, operation_plan_lease_id, authority_source,
			created_at, completed_at
		FROM capability_invocations
		WHERE grant_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, grantID, limit)
	if err != nil {
		return nil, fmt.Errorf("query capability invocations for grant %q: %w", grantID, err)
	}
	defer rows.Close()
	return scanCapabilityInvocationRows(rows)
}

func capabilityGrantAllowsAction(grant CapabilityGrant, action string) bool {
	action = normalizeEnumValue(action)
	for _, allowed := range NormalizeCapabilityActions(grant.AllowedActions) {
		if allowed == "*" || allowed == action {
			return true
		}
	}
	return false
}

func capabilityGrantSelectSQL() string {
	return `
		SELECT grant_id, request_id, granted_by, granted_to, kind, target_resource, allowed_actions_json,
			contract_json, constraints_json, status, baseline_policy_hash, current_policy_hash, anchor_fingerprint,
			drift_source, stale_reason, invocation_count, failure_count, created_at, updated_at, granted_at,
			expires_at, revoked_at, last_invoked_at, last_failure_at
		FROM capability_grants
	`
}

func scanCapabilityRequest(scanner interface{ Scan(dest ...any) error }) (CapabilityRequest, error) {
	var (
		request      CapabilityRequest
		kindRaw      string
		statusRaw    string
		createdAtRaw string
		updatedAtRaw string
	)
	if err := scanner.Scan(
		&request.RequestID,
		&request.RequestedBy,
		&request.RequestedFor,
		&request.ParentPrincipal,
		&request.AdminPrincipal,
		&kindRaw,
		&request.TargetResource,
		&request.Purpose,
		&request.RiskClass,
		&request.Contract,
		&request.Constraints,
		&statusRaw,
		&request.GrantID,
		&createdAtRaw,
		&updatedAtRaw,
	); err != nil {
		return CapabilityRequest{}, err
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return CapabilityRequest{}, fmt.Errorf("parse capability request created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return CapabilityRequest{}, fmt.Errorf("parse capability request updated_at: %w", err)
	}
	request.Kind = CapabilityKind(kindRaw)
	request.ReviewStatus = CapabilityReviewStatus(statusRaw)
	request.CreatedAt = createdAt
	request.UpdatedAt = updatedAt
	return NormalizeCapabilityRequest(request), nil
}

func scanCapabilityInvocationRows(rows *sql.Rows) ([]CapabilityInvocation, error) {
	invocations := []CapabilityInvocation{}
	for rows.Next() {
		invocation, err := scanCapabilityInvocation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan capability invocation: %w", err)
		}
		invocations = append(invocations, invocation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate capability invocations: %w", err)
	}
	return invocations, nil
}

func scanCapabilityInvocation(scanner interface{ Scan(dest ...any) error }) (CapabilityInvocation, error) {
	var (
		invocation      CapabilityInvocation
		createdRaw      string
		completedRaw    sql.NullString
		statusRaw       string
		outcomeRaw      string
		actionRaw       string
		sourceRaw       string
		errorText       string
		outcomeErrorRaw string
		sessionID       string
		leaseID         string
		planLeaseID     string
	)
	if err := scanner.Scan(
		&invocation.InvocationID,
		&invocation.GrantID,
		&invocation.Principal,
		&actionRaw,
		&statusRaw,
		&errorText,
		&outcomeRaw,
		&outcomeErrorRaw,
		&sessionID,
		&invocation.TurnRunID,
		&leaseID,
		&planLeaseID,
		&sourceRaw,
		&createdRaw,
		&completedRaw,
	); err != nil {
		return CapabilityInvocation{}, err
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return CapabilityInvocation{}, fmt.Errorf("parse capability invocation created_at: %w", err)
	}
	invocation.Action = actionRaw
	invocation.Status = statusRaw
	invocation.ErrorText = errorText
	invocation.OutcomeStatus = outcomeRaw
	invocation.OutcomeErrorText = outcomeErrorRaw
	invocation.SessionID = sessionID
	invocation.ContinuationLeaseID = leaseID
	invocation.OperationPlanLeaseID = planLeaseID
	invocation.AuthoritySource = sourceRaw
	invocation.CreatedAt = createdAt
	if completedRaw.Valid && strings.TrimSpace(completedRaw.String) != "" {
		completedAt, err := parseSQLiteTime(completedRaw.String)
		if err != nil {
			return CapabilityInvocation{}, fmt.Errorf("parse capability invocation completed_at: %w", err)
		}
		invocation.CompletedAt = completedAt
	}
	return NormalizeCapabilityInvocation(invocation), nil
}

func scanCapabilityReview(scanner interface{ Scan(dest ...any) error }) (CapabilityReview, error) {
	var (
		review       CapabilityReview
		statusRaw    string
		createdAtRaw string
	)
	if err := scanner.Scan(
		&review.ReviewID,
		&review.RequestID,
		&review.Reviewer,
		&review.ReviewerRole,
		&statusRaw,
		&review.Rationale,
		&createdAtRaw,
	); err != nil {
		return CapabilityReview{}, err
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return CapabilityReview{}, fmt.Errorf("parse capability review created_at: %w", err)
	}
	review.Status = CapabilityReviewStatus(statusRaw)
	review.CreatedAt = createdAt
	return NormalizeCapabilityReview(review), nil
}

func scanCapabilityGrant(scanner interface{ Scan(dest ...any) error }) (CapabilityGrant, error) {
	var (
		grant            CapabilityGrant
		kindRaw          string
		statusRaw        string
		actionsRaw       string
		driftSourceRaw   string
		createdAtRaw     string
		updatedAtRaw     string
		grantedAtRaw     sql.NullString
		expiresAtRaw     sql.NullString
		revokedAtRaw     sql.NullString
		lastInvokedAtRaw sql.NullString
		lastFailureAtRaw sql.NullString
	)
	if err := scanner.Scan(
		&grant.GrantID,
		&grant.RequestID,
		&grant.GrantedBy,
		&grant.GrantedTo,
		&kindRaw,
		&grant.TargetResource,
		&actionsRaw,
		&grant.Contract,
		&grant.Constraints,
		&statusRaw,
		&grant.BaselinePolicyHash,
		&grant.CurrentPolicyHash,
		&grant.AnchorFingerprint,
		&driftSourceRaw,
		&grant.StaleReason,
		&grant.InvocationCount,
		&grant.FailureCount,
		&createdAtRaw,
		&updatedAtRaw,
		&grantedAtRaw,
		&expiresAtRaw,
		&revokedAtRaw,
		&lastInvokedAtRaw,
		&lastFailureAtRaw,
	); err != nil {
		return CapabilityGrant{}, err
	}
	actions, err := unmarshalStringSlice(actionsRaw)
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("decode capability grant actions: %w", err)
	}
	grant.AllowedActions = actions
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("parse capability grant created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("parse capability grant updated_at: %w", err)
	}
	grant.Kind = CapabilityKind(kindRaw)
	grant.Status = CapabilityGrantStatus(statusRaw)
	grant.DriftSource = ToolDriftSource(strings.TrimSpace(driftSourceRaw))
	grant.CreatedAt = createdAt
	grant.UpdatedAt = updatedAt
	if grantedAtRaw.Valid && strings.TrimSpace(grantedAtRaw.String) != "" {
		grant.GrantedAt, err = parseSQLiteTime(grantedAtRaw.String)
		if err != nil {
			return CapabilityGrant{}, fmt.Errorf("parse capability grant granted_at: %w", err)
		}
	}
	if expiresAtRaw.Valid && strings.TrimSpace(expiresAtRaw.String) != "" {
		grant.ExpiresAt, err = parseSQLiteTime(expiresAtRaw.String)
		if err != nil {
			return CapabilityGrant{}, fmt.Errorf("parse capability grant expires_at: %w", err)
		}
	}
	if revokedAtRaw.Valid && strings.TrimSpace(revokedAtRaw.String) != "" {
		grant.RevokedAt, err = parseSQLiteTime(revokedAtRaw.String)
		if err != nil {
			return CapabilityGrant{}, fmt.Errorf("parse capability grant revoked_at: %w", err)
		}
	}
	if lastInvokedAtRaw.Valid && strings.TrimSpace(lastInvokedAtRaw.String) != "" {
		grant.LastInvokedAt, err = parseSQLiteTime(lastInvokedAtRaw.String)
		if err != nil {
			return CapabilityGrant{}, fmt.Errorf("parse capability grant last_invoked_at: %w", err)
		}
	}
	if lastFailureAtRaw.Valid && strings.TrimSpace(lastFailureAtRaw.String) != "" {
		grant.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
		if err != nil {
			return CapabilityGrant{}, fmt.Errorf("parse capability grant last_failure_at: %w", err)
		}
	}
	return NormalizeCapabilityGrant(grant), nil
}
