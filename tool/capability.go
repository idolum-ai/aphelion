//go:build linux

package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) capabilityRequest(_ context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("capability_request requires transcript store")
	}
	if p.Role == "" {
		return "", fmt.Errorf("capability_request requires an authenticated principal")
	}
	var in capabilityInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode capability_request input: %w", err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "":
		return renderCapabilityRequestHelp(), nil
	case "request_submit":
		return r.capabilityRequestSubmit(in, p, key)
	case "request_show":
		return r.capabilityRequestShow(in, p)
	case "request_list":
		return r.capabilityRequestList(in, p)
	default:
		return "", fmt.Errorf("capability_request action %q is not supported", strings.TrimSpace(in.Action))
	}
}

func (r *Registry) capabilityAuthority(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("capability_authority requires transcript store")
	}
	if p.Role == "" {
		return "", fmt.Errorf("capability_authority requires an authenticated principal")
	}
	var in capabilityInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode capability_authority input: %w", err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "":
		return renderCapabilityAuthorityHelp(), nil
	case "request_show":
		return r.capabilityAuthorityRequestShow(in, p)
	case "request_list":
		return r.capabilityAuthorityRequestList(in, p)
	case "request_review":
		return r.capabilityAuthorityRequestReview(in, p, key)
	case "grant_set":
		return r.capabilityAuthorityGrantSet(ctx, in, p, key)
	case "grant_show":
		return r.capabilityAuthorityGrantShow(in, p)
	case "grant_list":
		return r.capabilityAuthorityGrantList(in, p)
	case "grant_revoke":
		return r.capabilityAuthorityGrantRevoke(in, p, key)
	case "access_check":
		return r.capabilityAuthorityAccessCheck(in)
	default:
		return "", fmt.Errorf("capability_authority action %q is not supported", strings.TrimSpace(in.Action))
	}
}

func (r *Registry) capabilityRequestSubmit(in capabilityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		requestID = generatedOperationID("cap")
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(in.Kind))
	if kind == "" {
		return "", fmt.Errorf("capability_request request_submit requires kind")
	}
	target := strings.TrimSpace(in.TargetResource)
	if target == "" {
		return "", fmt.Errorf("capability_request request_submit requires target_resource")
	}
	purpose := strings.TrimSpace(in.Purpose)
	if purpose == "" {
		return "", fmt.Errorf("capability_request request_submit requires purpose")
	}
	contract, err := normalizeCapabilityJSONBlob(in.Contract, "contract")
	if err != nil {
		return "", err
	}
	contract, err = mergeCapabilityUpdatePlanIntoContract(contract, capabilityUpdatePlanFromCapabilityInput(in))
	if err != nil {
		return "", err
	}
	constraints, err := normalizeCapabilityJSONBlob(in.Constraints, "constraints")
	if err != nil {
		return "", err
	}
	if err := validateCapabilityChildRuntimeContract(contract, constraints); err != nil {
		return "", err
	}
	requester := toolAuthorityPrincipalDisplay(actor)
	requestedFor := canonicalDurableAgentPrincipalIfKnown(r.store, firstNonEmpty(strings.TrimSpace(in.RequestedFor), requester))
	record, err := r.store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:       requestID,
		RequestedBy:     requester,
		RequestedFor:    requestedFor,
		ParentPrincipal: strings.TrimSpace(in.ParentPrincipal),
		AdminPrincipal:  strings.TrimSpace(in.AdminPrincipal),
		Kind:            kind,
		TargetResource:  target,
		Purpose:         purpose,
		RiskClass:       strings.TrimSpace(in.RiskClass),
		Contract:        contract,
		Constraints:     constraints,
		ReviewStatus:    session.CapabilityReviewStatusProposed,
	})
	if err != nil {
		return "", err
	}
	if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityRequestCreated, string(record.ReviewStatus), map[string]any{
		"request_id":        record.RequestID,
		"kind":              string(record.Kind),
		"target_resource":   record.TargetResource,
		"review_status":     string(record.ReviewStatus),
		"requested_by":      record.RequestedBy,
		"requested_for":     record.RequestedFor,
		"parent_principal":  record.ParentPrincipal,
		"requester_role":    strings.TrimSpace(string(actor.Role)),
		"requester_user_id": actor.TelegramUserID,
		"request_via":       "capability_request",
	}); err != nil {
		return "", err
	}
	reviewEventID := int64(0)
	if in.ReviewTargetChatID > 0 {
		reviewEventID, err = r.queueCapabilityRequestReviewEvent(record, in, actor, key)
		if err != nil {
			return "", err
		}
	}
	return renderCapabilityRequestWithReviewEvent("[CAPABILITY_REQUEST]", record, reviewEventID), nil
}

func (r *Registry) capabilityRequestShow(in capabilityInput, actor principal.Principal) (string, error) {
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		return "", fmt.Errorf("capability_request request_show requires request_id")
	}
	record, ok, err := r.store.CapabilityRequest(requestID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("capability request %q not found", requestID)
	}
	if !capabilityRequestVisibleTo(actor, record) {
		return "", fmt.Errorf("capability request %q is not visible to principal %q", requestID, toolAuthorityPrincipalDisplay(actor))
	}
	return renderCapabilityRequest("[CAPABILITY_REQUEST]", record), nil
}

func (r *Registry) capabilityRequestList(in capabilityInput, actor principal.Principal) (string, error) {
	limit := boundedLimit(in.Limit, 50)
	status := session.NormalizeCapabilityReviewStatus(session.CapabilityReviewStatus(in.ReviewStatus))
	if strings.TrimSpace(in.ReviewStatus) != "" && status == "" {
		return "", fmt.Errorf("capability_request review_status must be proposed, parent_approved, approved, or rejected")
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(in.Kind))
	if strings.TrimSpace(in.Kind) != "" && kind == "" {
		return "", fmt.Errorf("capability_request kind is not supported")
	}
	records, err := r.store.CapabilityRequests(200, status, kind, "")
	if err != nil {
		return "", err
	}
	filtered := make([]session.CapabilityRequest, 0, limit)
	for _, record := range records {
		if !capabilityRequestVisibleTo(actor, record) {
			continue
		}
		filtered = append(filtered, record)
		if len(filtered) >= limit {
			break
		}
	}
	return renderCapabilityRequestList(filtered), nil
}

func (r *Registry) capabilityAuthorityRequestShow(in capabilityInput, actor principal.Principal) (string, error) {
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		return "", fmt.Errorf("capability_authority request_show requires request_id")
	}
	record, ok, err := r.store.CapabilityRequest(requestID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("capability request %q not found", requestID)
	}
	if !capabilityRequestVisibleTo(actor, record) {
		return "", fmt.Errorf("capability request %q is not visible to principal %q", requestID, toolAuthorityPrincipalDisplay(actor))
	}
	return renderCapabilityRequest("[CAPABILITY_REQUEST]", record), nil
}

func (r *Registry) capabilityAuthorityRequestList(in capabilityInput, actor principal.Principal) (string, error) {
	limit := boundedLimit(in.Limit, 100)
	status := session.NormalizeCapabilityReviewStatus(session.CapabilityReviewStatus(in.ReviewStatus))
	if strings.TrimSpace(in.ReviewStatus) != "" && status == "" {
		return "", fmt.Errorf("capability_authority review_status must be proposed, parent_approved, approved, or rejected")
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(in.Kind))
	if strings.TrimSpace(in.Kind) != "" && kind == "" {
		return "", fmt.Errorf("capability_authority kind is not supported")
	}
	principalFilter := ""
	if actor.Role != principal.RoleAdmin {
		principalFilter = toolAuthorityPrincipalDisplay(actor)
	}
	records, err := r.store.CapabilityRequests(limit, status, kind, principalFilter)
	if err != nil {
		return "", err
	}
	if actor.Role != principal.RoleAdmin {
		filtered := records[:0]
		for _, record := range records {
			if capabilityRequestVisibleTo(actor, record) {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}
	return renderCapabilityRequestList(records), nil
}

func (r *Registry) capabilityAuthorityRequestReview(in capabilityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		return "", fmt.Errorf("capability_authority request_review requires request_id")
	}
	status := session.NormalizeCapabilityReviewStatus(session.CapabilityReviewStatus(in.ReviewStatus))
	if status == "" || status == session.CapabilityReviewStatusProposed {
		return "", fmt.Errorf("capability_authority request_review requires review_status parent_approved, approved, or rejected")
	}
	record, ok, err := r.store.CapabilityRequest(requestID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("capability request %q not found", requestID)
	}
	switch status {
	case session.CapabilityReviewStatusApproved:
		if actor.Role != principal.RoleAdmin {
			return "", fmt.Errorf("capability_authority request_review approved is admin-only")
		}
		if strings.TrimSpace(record.ParentPrincipal) != "" && record.ReviewStatus == session.CapabilityReviewStatusProposed {
			err := fmt.Errorf("capability_authority request_review approved requires parent_approved first for parent_principal %q", record.ParentPrincipal)
			return renderCapabilityBlocked("parent_approval_required", err.Error(), []string{
				fmt.Sprintf("Ask %s to run capability_authority request_review with review_status=parent_approved.", record.ParentPrincipal),
				"After parent_approved is recorded, retry admin approval with review_status=approved.",
			}), err
		}
	case session.CapabilityReviewStatusParentApproved:
		if actor.Role != principal.RoleAdmin && !capabilityPrincipalMatches(actor, record.ParentPrincipal) {
			return "", fmt.Errorf("capability_authority request_review parent_approved requires parent_principal %q", record.ParentPrincipal)
		}
		if actor.Role != principal.RoleAdmin && strings.TrimSpace(record.ParentPrincipal) == "" {
			return "", fmt.Errorf("capability_authority request_review parent_approved requires request parent_principal")
		}
	case session.CapabilityReviewStatusRejected:
		if actor.Role != principal.RoleAdmin && !capabilityPrincipalMatches(actor, record.ParentPrincipal) {
			return "", fmt.Errorf("capability_authority request_review rejected requires admin or parent_principal %q", record.ParentPrincipal)
		}
	}
	reviewID := generatedOperationID("capr")
	review, err := r.store.AppendCapabilityReview(session.CapabilityReview{
		ReviewID:     reviewID,
		RequestID:    record.RequestID,
		Reviewer:     toolAuthorityPrincipalDisplay(actor),
		ReviewerRole: string(actor.Role),
		Status:       status,
		Rationale:    strings.TrimSpace(in.Rationale),
	})
	if err != nil {
		return "", err
	}
	updated, _, err := r.store.CapabilityRequest(record.RequestID)
	if err != nil {
		return "", err
	}
	if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityReviewed, string(review.Status), map[string]any{
		"request_id":      record.RequestID,
		"review_id":       review.ReviewID,
		"review_status":   string(review.Status),
		"reviewer":        review.Reviewer,
		"reviewer_role":   review.ReviewerRole,
		"kind":            string(record.Kind),
		"target_resource": record.TargetResource,
	}); err != nil {
		return "", err
	}
	return renderCapabilityRequest("[CAPABILITY_REQUEST_REVIEWED]", updated), nil
}

func (r *Registry) capabilityAuthorityGrantSet(ctx context.Context, in capabilityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	if actor.Role != principal.RoleAdmin {
		return "", fmt.Errorf("capability_authority grant_set is admin-only")
	}
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		return "", fmt.Errorf("capability_authority grant_set requires request_id")
	}
	request, ok, err := r.store.CapabilityRequest(requestID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("capability request %q not found", requestID)
	}
	if request.ReviewStatus != session.CapabilityReviewStatusApproved {
		return "", fmt.Errorf("capability_authority grant_set requires approved request; current status is %s", request.ReviewStatus)
	}
	grantID := strings.TrimSpace(in.GrantID)
	if grantID == "" {
		grantID = generatedOperationID("capg")
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(firstNonEmpty(in.Kind, string(request.Kind))))
	if kind == "" {
		return "", fmt.Errorf("capability_authority grant_set requires kind")
	}
	target := firstNonEmpty(in.TargetResource, request.TargetResource)
	if target == "" {
		return "", fmt.Errorf("capability_authority grant_set requires target_resource")
	}
	grantedTo := canonicalDurableAgentPrincipalIfKnown(r.store, firstNonEmpty(in.Principal, request.RequestedFor, request.RequestedBy))
	if grantedTo == "" {
		return "", fmt.Errorf("capability_authority grant_set requires principal")
	}
	actions := session.NormalizeCapabilityActions(in.AllowedActions)
	if len(actions) == 0 {
		if action := strings.TrimSpace(in.CapabilityAction); action != "" {
			actions = session.NormalizeCapabilityActions([]string{action})
		}
	}
	contract, err := normalizeCapabilityJSONBlobWithDefault(in.Contract, "contract", request.Contract)
	if err != nil {
		return "", err
	}
	contract, err = mergeCapabilityUpdatePlanIntoContract(contract, capabilityUpdatePlanFromCapabilityInput(in))
	if err != nil {
		return "", err
	}
	plan, hasPlan, err := capabilityUpdatePlanFromContract(contract)
	if err != nil {
		return "", err
	}
	if len(actions) == 0 && hasPlan && len(plan.GrantActions) > 0 {
		actions = session.NormalizeCapabilityActions(plan.GrantActions)
	}
	if len(actions) == 0 {
		actions = []string{"invoke"}
	}
	constraints, err := normalizeCapabilityJSONBlobWithDefault(in.Constraints, "constraints", request.Constraints)
	if err != nil {
		return "", err
	}
	if err := validateCapabilityChildRuntimeContract(contract, constraints); err != nil {
		return "", err
	}
	status := session.NormalizeCapabilityGrantStatus(session.CapabilityGrantStatus(in.GrantStatus))
	if strings.TrimSpace(in.GrantStatus) != "" && status == "" {
		return "", fmt.Errorf("capability_authority grant_status must be pending, active, stale, revoked, expired, or failed")
	}
	if status == "" {
		status = session.CapabilityGrantStatusActive
	}
	now := time.Now().UTC()
	expiresAt := time.Time{}
	if in.ExpiresInSeconds > 0 {
		expiresAt = now.Add(time.Duration(in.ExpiresInSeconds) * time.Second)
	}
	policyHash := capabilityGrantPolicyHash(kind, target, grantedTo, actions, contract, constraints)
	grantRecord := session.CapabilityGrant{
		GrantID:            grantID,
		RequestID:          request.RequestID,
		GrantedBy:          toolAuthorityPrincipalDisplay(actor),
		GrantedTo:          grantedTo,
		Kind:               kind,
		TargetResource:     target,
		AllowedActions:     actions,
		Contract:           contract,
		Constraints:        constraints,
		Status:             status,
		BaselinePolicyHash: policyHash,
		CurrentPolicyHash:  policyHash,
		AnchorFingerprint:  policyHash,
		GrantedAt:          now,
		ExpiresAt:          expiresAt,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	requiresPolicyApply := status == session.CapabilityGrantStatusActive && hasPlan && capabilityUpdatePlanHasDurablePolicyPatch(plan)
	if requiresPolicyApply {
		grantRecord.Status = session.CapabilityGrantStatusPending
		grantRecord.GrantedAt = time.Time{}
	}
	grant, err := r.store.UpsertCapabilityGrant(grantRecord)
	if err != nil {
		return "", err
	}
	var updateResult *capabilityUpdatePlanApplyResult
	if requiresPolicyApply {
		updateResult, err = r.applyCapabilityUpdatePlanForGrant(request, grantRecord)
		if err != nil {
			failed := grant
			failed.Status = session.CapabilityGrantStatusFailed
			failed.FailureCount++
			failed.LastFailureAt = time.Now().UTC()
			failed.StaleReason = "capability_update_plan_apply_failed: " + err.Error()
			failed.UpdatedAt = failed.LastFailureAt
			if stored, storeErr := r.store.UpsertCapabilityGrant(failed); storeErr == nil {
				grant = stored
			}
			_ = r.appendCapabilityEvent(key, core.ExecutionEventCapabilityGrantChanged, string(session.CapabilityGrantStatusFailed), map[string]any{
				"grant_id":        grant.GrantID,
				"request_id":      grant.RequestID,
				"kind":            string(grant.Kind),
				"target_resource": grant.TargetResource,
				"granted_to":      grant.GrantedTo,
				"status":          string(session.CapabilityGrantStatusFailed),
				"failure_reason":  failed.StaleReason,
			})
			return renderCapabilityGrantFailure(grant, err), err
		}
		grantRecord.Status = session.CapabilityGrantStatusActive
		grantRecord.GrantedAt = time.Now().UTC()
		grantRecord.CreatedAt = grant.CreatedAt
		grantRecord.UpdatedAt = grantRecord.GrantedAt
		grant, err = r.store.UpsertCapabilityGrant(grantRecord)
		if err != nil {
			return "", err
		}
	}
	if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityGrantChanged, string(grant.Status), map[string]any{
		"grant_id":        grant.GrantID,
		"request_id":      grant.RequestID,
		"kind":            string(grant.Kind),
		"target_resource": grant.TargetResource,
		"granted_to":      grant.GrantedTo,
		"granted_by":      grant.GrantedBy,
		"status":          string(grant.Status),
		"allowed_actions": grant.AllowedActions,
	}); err != nil {
		return "", err
	}
	if updateResult != nil {
		if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityUpdateApplied, string(grant.Status), map[string]any{
			"grant_id":              grant.GrantID,
			"request_id":            grant.RequestID,
			"agent_id":              updateResult.AgentID,
			"policy_update_applied": updateResult.PolicyUpdateApplied,
			"policy_changed":        updateResult.PolicyChanged,
			"policy_update_id":      updateResult.PolicyUpdateID,
			"policy_version":        updateResult.PolicyVersion,
			"policy_hash":           updateResult.PolicyHash,
		}); err != nil {
			return "", err
		}
	}
	if grant.Status == session.CapabilityGrantStatusActive && r.capabilityGrantObserver != nil {
		r.capabilityGrantObserver(ctx, key, grant)
	}
	return renderCapabilityGrantWithUpdate("[CAPABILITY_GRANT]", grant, updateResult), nil
}

func (r *Registry) capabilityAuthorityGrantShow(in capabilityInput, actor principal.Principal) (string, error) {
	grantID := strings.TrimSpace(in.GrantID)
	if grantID == "" {
		return "", fmt.Errorf("capability_authority grant_show requires grant_id")
	}
	grant, ok, err := r.store.CapabilityGrant(grantID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("capability grant %q not found", grantID)
	}
	if !r.capabilityGrantVisibleTo(actor, grant) {
		return "", fmt.Errorf("capability grant %q is not visible to principal %q", grantID, toolAuthorityPrincipalDisplay(actor))
	}
	return renderCapabilityGrant("[CAPABILITY_GRANT]", grant), nil
}

func (r *Registry) capabilityAuthorityGrantList(in capabilityInput, actor principal.Principal) (string, error) {
	limit := boundedLimit(in.Limit, 100)
	status := session.NormalizeCapabilityGrantStatus(session.CapabilityGrantStatus(in.GrantStatus))
	if strings.TrimSpace(in.GrantStatus) != "" && status == "" {
		return "", fmt.Errorf("capability_authority grant_status must be pending, active, stale, revoked, expired, or failed")
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(in.Kind))
	if strings.TrimSpace(in.Kind) != "" && kind == "" {
		return "", fmt.Errorf("capability_authority kind is not supported")
	}
	records, err := r.store.CapabilityGrants(200, status, kind, "")
	if err != nil {
		return "", err
	}
	filtered := make([]session.CapabilityGrant, 0, limit)
	for _, record := range records {
		if !r.capabilityGrantVisibleTo(actor, record) {
			continue
		}
		filtered = append(filtered, record)
		if len(filtered) >= limit {
			break
		}
	}
	return renderCapabilityGrantList(filtered), nil
}

func (r *Registry) capabilityAuthorityGrantRevoke(in capabilityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	if actor.Role != principal.RoleAdmin {
		return "", fmt.Errorf("capability_authority grant_revoke is admin-only")
	}
	grantID := strings.TrimSpace(in.GrantID)
	if grantID == "" {
		return "", fmt.Errorf("capability_authority grant_revoke requires grant_id")
	}
	grant, ok, err := r.store.CapabilityGrant(grantID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("capability grant %q not found", grantID)
	}
	now := time.Now().UTC()
	grant.Status = session.CapabilityGrantStatusRevoked
	grant.StaleReason = strings.TrimSpace(in.Rationale)
	grant.RevokedAt = now
	grant.UpdatedAt = now
	grant, err = r.store.UpsertCapabilityGrant(grant)
	if err != nil {
		return "", err
	}
	if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityGrantChanged, string(grant.Status), map[string]any{
		"grant_id":        grant.GrantID,
		"request_id":      grant.RequestID,
		"kind":            string(grant.Kind),
		"target_resource": grant.TargetResource,
		"granted_to":      grant.GrantedTo,
		"status":          string(grant.Status),
		"revoked_by":      toolAuthorityPrincipalDisplay(actor),
		"rationale":       strings.TrimSpace(in.Rationale),
	}); err != nil {
		return "", err
	}
	return renderCapabilityGrant("[CAPABILITY_GRANT_REVOKED]", grant), nil
}

func (r *Registry) capabilityAuthorityAccessCheck(in capabilityInput) (string, error) {
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(in.Kind))
	if kind == "" {
		kind = session.CapabilityKindTool
	}
	target := strings.TrimSpace(in.TargetResource)
	if target == "" {
		return "", fmt.Errorf("capability_authority access_check requires target_resource")
	}
	principalID := canonicalDurableAgentPrincipalIfKnown(r.store, strings.TrimSpace(in.Principal))
	if principalID == "" {
		return "", fmt.Errorf("capability_authority access_check requires principal")
	}
	action := firstNonEmpty(strings.TrimSpace(in.CapabilityAction), "invoke")
	grant, allowed, err := r.store.ActiveCapabilityGrant(kind, target, principalID, action)
	if err != nil {
		return "", err
	}
	return renderCapabilityAccess(kind, target, principalID, action, allowed, grant), nil
}

func (r *Registry) capabilityGrantVisibleTo(actor principal.Principal, grant session.CapabilityGrant) bool {
	if actor.Role == principal.RoleAdmin {
		return true
	}
	if capabilityPrincipalMatches(actor, grant.GrantedTo) || capabilityPrincipalMatches(actor, grant.GrantedBy) {
		return true
	}
	if strings.TrimSpace(grant.RequestID) == "" || r == nil || r.store == nil {
		return false
	}
	request, ok, err := r.store.CapabilityRequest(grant.RequestID)
	if err != nil || !ok {
		return false
	}
	return capabilityRequestVisibleTo(actor, request)
}

func capabilityRequestVisibleTo(actor principal.Principal, request session.CapabilityRequest) bool {
	if actor.Role == principal.RoleAdmin {
		return true
	}
	return capabilityPrincipalMatches(actor, request.RequestedBy) ||
		capabilityPrincipalMatches(actor, request.RequestedFor) ||
		capabilityPrincipalMatches(actor, request.ParentPrincipal) ||
		capabilityPrincipalMatches(actor, request.AdminPrincipal)
}

func capabilityPrincipalMatches(actor principal.Principal, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if target == toolAuthorityPrincipalDisplay(actor) {
		return true
	}
	for _, key := range toolAuthorityPrincipalKeys(actor) {
		if target == key {
			return true
		}
	}
	return false
}

func normalizeCapabilityJSONBlob(raw json.RawMessage, field string) (string, error) {
	return normalizeCapabilityJSONBlobWithDefault(raw, field, "{}")
}

func normalizeCapabilityJSONBlobWithDefault(raw json.RawMessage, field string, fallback string) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		if strings.TrimSpace(fallback) == "" {
			return "{}", nil
		}
		return strings.TrimSpace(fallback), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("capability %s must be valid json", strings.TrimSpace(field))
	}
	return trimmed, nil
}

func capabilityGrantPolicyHash(kind session.CapabilityKind, target string, principalID string, actions []string, contract string, constraints string) string {
	payload := map[string]any{
		"kind":            string(kind),
		"target_resource": strings.TrimSpace(target),
		"principal":       strings.TrimSpace(principalID),
		"allowed_actions": session.NormalizeCapabilityActions(actions),
		"contract":        strings.TrimSpace(contract),
		"constraints":     strings.TrimSpace(constraints),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Registry) appendCapabilityEvent(key session.SessionKey, eventType string, status string, payload map[string]any) error {
	return r.appendToolLifecycleEvent(key, "capability_delegation", eventType, status, payload)
}

type capabilityUpdatePlanApplyResult struct {
	PolicyUpdateApplied bool
	PolicyChanged       bool
	AgentID             string
	PolicyVersion       int64
	PolicyHash          string
	PolicyUpdateID      int64
}

func (r *Registry) applyCapabilityUpdatePlanForGrant(request session.CapabilityRequest, grant session.CapabilityGrant) (*capabilityUpdatePlanApplyResult, error) {
	plan, hasPlan, err := capabilityUpdatePlanFromContract(grant.Contract)
	if err != nil {
		return nil, err
	}
	if !hasPlan || !capabilityUpdatePlanHasDurablePolicyPatch(plan) {
		return nil, nil
	}
	agentID, err := r.resolveCapabilityUpdatePlanAgentID(plan, request, grant)
	if err != nil {
		return nil, err
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return nil, err
	}
	patch := effectiveDurableAgentPolicyPatchFromInput(durableAgentInput{
		PolicyPatch:     plan.PolicyPatch,
		PolicyOverrides: plan.PolicyOverrides,
	})
	policy := agent.LivePolicy
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(plan.Reason)
	if reason == "" {
		reason = fmt.Sprintf("applied capability_update_plan from grant %s for request %s", grant.GrantID, request.RequestID)
	}
	updated, update, err := r.store.ApplyDurableAgentLivePolicy(agent.AgentID, policy, 0, reason)
	if err != nil {
		return nil, err
	}
	result := &capabilityUpdatePlanApplyResult{
		PolicyUpdateApplied: true,
		AgentID:             updated.AgentID,
		PolicyVersion:       updated.PolicyVersion,
		PolicyHash:          updated.PolicyHash,
	}
	if update != nil {
		result.PolicyChanged = true
		result.PolicyUpdateID = update.ID
	}
	return result, nil
}

func (r *Registry) resolveCapabilityUpdatePlanAgentID(plan capabilityUpdatePlanInput, request session.CapabilityRequest, grant session.CapabilityGrant) (string, error) {
	plan = normalizeCapabilityUpdatePlan(plan)
	if plan.AgentID != "" {
		return plan.AgentID, nil
	}
	candidates := []string{
		strings.TrimPrefix(strings.TrimSpace(request.TargetResource), "durable_agent:"),
		strings.TrimPrefix(strings.TrimSpace(grant.TargetResource), "durable_agent:"),
		durableAgentIDFromCapabilityPrincipal(request.RequestedFor),
		durableAgentIDFromCapabilityPrincipal(grant.GrantedTo),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, err := r.store.DurableAgent(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("capability_update_plan with policy_patch requires agent_id")
}

func (r *Registry) queueCapabilityRequestReviewEvent(record session.CapabilityRequest, in capabilityInput, actor principal.Principal, key session.SessionKey) (int64, error) {
	if r == nil || r.store == nil {
		return 0, fmt.Errorf("capability_request review notification requires transcript store")
	}
	record = session.NormalizeCapabilityRequest(record)
	if in.ReviewTargetChatID <= 0 {
		return 0, nil
	}
	metadata := map[string]any{
		"request_id":       record.RequestID,
		"kind":             string(record.Kind),
		"target_resource":  record.TargetResource,
		"review_status":    string(record.ReviewStatus),
		"requested_by":     record.RequestedBy,
		"requested_for":    record.RequestedFor,
		"parent_principal": record.ParentPrincipal,
		"admin_principal":  record.AdminPrincipal,
		"risk_class":       record.RiskClass,
		"purpose":          record.Purpose,
		"request_via":      "capability_request",
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return 0, fmt.Errorf("marshal capability request review metadata: %w", err)
	}
	sourceScope := key.Scope
	if sourceScope.IsZero() {
		sourceScope = capabilityRequestActorScope(actor)
	}
	summary := strings.TrimSpace(in.ReviewSummary)
	if summary == "" {
		summary = capabilityRequestReviewSummary(record)
	}
	return r.store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      key.ChatID,
		SourceUserID:      actor.TelegramUserID,
		SourceRole:        "capability_request",
		SourceScope:       sourceScope,
		TargetAdminChatID: in.ReviewTargetChatID,
		TargetScope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   fmt.Sprintf("%d", in.ReviewTargetChatID),
		},
		Summary:      summary,
		MetadataJSON: string(raw),
	})
}

func capabilityRequestActorScope(actor principal.Principal) session.ScopeRef {
	switch actor.Role {
	case principal.RoleDurableAgent:
		if id := strings.TrimSpace(actor.DurableAgentID); id != "" {
			return session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: id, DurableAgentID: id}
		}
	case principal.RoleAdmin, principal.RoleApprovedUser:
		if actor.TelegramUserID > 0 {
			id := fmt.Sprintf("%d", actor.TelegramUserID)
			return session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: id}
		}
	}
	return session.ScopeRef{}
}

func capabilityRequestReviewSummary(record session.CapabilityRequest) string {
	record = session.NormalizeCapabilityRequest(record)
	parts := []string{
		fmt.Sprintf("capability_request=%s", record.RequestID),
		fmt.Sprintf("kind=%s", record.Kind),
		fmt.Sprintf("target=%s", firstNonEmpty(record.TargetResource, "-")),
		fmt.Sprintf("requested_for=%s", firstNonEmpty(record.RequestedFor, "-")),
	}
	lines := []string{strings.Join(parts, " ")}
	if record.Purpose != "" {
		lines = append(lines, "purpose: "+record.Purpose)
	}
	if record.RiskClass != "" {
		lines = append(lines, "risk: "+record.RiskClass)
	}
	return strings.Join(lines, "\n")
}

func boundedLimit(raw int, max int) int {
	if max <= 0 {
		max = 50
	}
	if raw <= 0 || raw > max {
		return max
	}
	return raw
}

func validateCapabilityChildRuntimeContract(contract string, constraints string) error {
	for _, raw := range []string{contract, constraints} {
		if capabilityJSONBlobHasKey(raw, "runtime_materialization") {
			return fmt.Errorf("capability contract must use child_runtime; runtime_materialization is migration-only")
		}
	}
	_, _, err := core.ExtractChildRuntimeContract(contract, constraints)
	if err != nil {
		return err
	}
	return nil
}

func capabilityJSONBlobHasKey(raw string, key string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return false
	}
	_, ok := obj[key]
	return ok
}

func renderCapabilityChildRuntime(b *strings.Builder, contract string, constraints string) {
	material, ok, err := core.ExtractChildRuntimeContract(contract, constraints)
	if err != nil || !ok {
		return
	}
	b.WriteString("child_runtime: present\n")
	if material.Executable != "" {
		fmt.Fprintf(b, "child_runtime_executable: %s\n", material.Executable)
	}
	if len(material.ReadonlyPaths) > 0 {
		fmt.Fprintf(b, "child_runtime_readonly_paths: %d\n", len(material.ReadonlyPaths))
	}
	if len(material.ReadonlyBinds) > 0 {
		fmt.Fprintf(b, "child_runtime_readonly_binds: %d\n", len(material.ReadonlyBinds))
	}
	if len(material.EnvFromParent) > 0 {
		fmt.Fprintf(b, "child_runtime_env_from_parent: %s\n", strings.Join(material.EnvFromParent, ","))
	}
}

func renderCapabilityRequestHelp() string {
	return strings.Join([]string{
		"[CAPABILITY_REQUEST]",
		"actions:",
		"- request_submit | request_show | request_list",
		"submits broad governed capability requests; parent/admin review and grants are handled through capability_authority",
	}, "\n")
}

func renderCapabilityAuthorityHelp() string {
	return strings.Join([]string{
		"[CAPABILITY_AUTHORITY]",
		"actions:",
		"- request_show | request_list | request_review",
		"- grant_set | grant_show | grant_list | grant_revoke",
		"- access_check",
	}, "\n")
}

func renderCapabilityRequest(header string, record session.CapabilityRequest) string {
	return renderCapabilityRequestWithReviewEvent(header, record, 0)
}

func renderCapabilityRequestWithReviewEvent(header string, record session.CapabilityRequest, reviewEventID int64) string {
	record = session.NormalizeCapabilityRequest(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "request_id: %s\n", record.RequestID)
	fmt.Fprintf(&b, "kind: %s\n", record.Kind)
	fmt.Fprintf(&b, "target_resource: %s\n", record.TargetResource)
	fmt.Fprintf(&b, "review_status: %s\n", record.ReviewStatus)
	fmt.Fprintf(&b, "requested_by: %s\n", firstNonEmpty(record.RequestedBy, "-"))
	fmt.Fprintf(&b, "requested_for: %s\n", firstNonEmpty(record.RequestedFor, "-"))
	if record.ParentPrincipal != "" {
		fmt.Fprintf(&b, "parent_principal: %s\n", record.ParentPrincipal)
	}
	if record.AdminPrincipal != "" {
		fmt.Fprintf(&b, "admin_principal: %s\n", record.AdminPrincipal)
	}
	if record.RiskClass != "" {
		fmt.Fprintf(&b, "risk_class: %s\n", record.RiskClass)
	}
	if record.Purpose != "" {
		fmt.Fprintf(&b, "purpose: %s\n", record.Purpose)
	}
	if record.GrantID != "" {
		fmt.Fprintf(&b, "grant_id: %s\n", record.GrantID)
	}
	if record.Contract != "" {
		fmt.Fprintf(&b, "contract: %s\n", record.Contract)
	}
	if record.Constraints != "" {
		fmt.Fprintf(&b, "constraints: %s\n", record.Constraints)
	}
	if reviewEventID > 0 {
		fmt.Fprintf(&b, "review_event_id: %d\n", reviewEventID)
	}
	return b.String()
}

func renderCapabilityRequestList(records []session.CapabilityRequest) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_REQUESTS]\n")
	fmt.Fprintf(&b, "count: %d\n", len(records))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeCapabilityRequest(record)
		fmt.Fprintf(&b, "- request_id=%s kind=%s target_resource=%s review_status=%s requested_for=%s parent_principal=%s\n", record.RequestID, record.Kind, firstNonEmpty(record.TargetResource, "-"), record.ReviewStatus, firstNonEmpty(record.RequestedFor, "-"), firstNonEmpty(record.ParentPrincipal, "-"))
	}
	return b.String()
}

func renderCapabilityBlocked(reason string, detail string, nextActions []string) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_BLOCKED]\n")
	fmt.Fprintf(&b, "status: blocked\n")
	if reason = strings.TrimSpace(reason); reason != "" {
		fmt.Fprintf(&b, "reason: %s\n", reason)
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		fmt.Fprintf(&b, "detail: %s\n", detail)
	}
	if len(nextActions) > 0 {
		b.WriteString("next_action:\n")
		for _, action := range nextActions {
			action = strings.TrimSpace(action)
			if action == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", action)
		}
	}
	return b.String()
}

func renderCapabilityGrant(header string, grant session.CapabilityGrant) string {
	return renderCapabilityGrantWithUpdate(header, grant, nil)
}

func renderCapabilityGrantFailure(grant session.CapabilityGrant, cause error) string {
	base := strings.TrimSpace(renderCapabilityGrant("[CAPABILITY_GRANT_FAILED]", grant))
	next := capabilityGrantFailureNextActions(cause)
	if len(next) == 0 {
		return base + "\n"
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\nnext_action:\n")
	for _, action := range next {
		fmt.Fprintf(&b, "- %s\n", action)
	}
	return b.String()
}

func capabilityGrantFailureNextActions(cause error) []string {
	var ceiling *core.DurableAgentPolicyCeilingError
	if errors.As(cause, &ceiling) && ceiling != nil {
		field := strings.TrimSpace(ceiling.Field)
		if field == "" {
			field = "live_policy"
		}
		return []string{
			fmt.Sprintf("The requested durable-agent policy exceeds the bootstrap ceiling for %s.", field),
			fmt.Sprintf("Requested: %s. Allowed: %s.", strings.Join(ceiling.Requested, ","), strings.Join(ceiling.Allowed, ",")),
			"Create an admin-reviewed request to widen the bootstrap ceiling, or retry grant_set with a policy inside the current ceiling.",
		}
	}
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		return []string{"Inspect stale_reason, adjust the grant contract or durable policy patch, then retry grant_set."}
	}
	return nil
}

func renderCapabilityGrantWithUpdate(header string, grant session.CapabilityGrant, update *capabilityUpdatePlanApplyResult) string {
	grant = session.NormalizeCapabilityGrant(grant)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "grant_id: %s\n", grant.GrantID)
	if grant.RequestID != "" {
		fmt.Fprintf(&b, "request_id: %s\n", grant.RequestID)
	}
	fmt.Fprintf(&b, "kind: %s\n", grant.Kind)
	fmt.Fprintf(&b, "target_resource: %s\n", grant.TargetResource)
	fmt.Fprintf(&b, "status: %s\n", grant.Status)
	fmt.Fprintf(&b, "granted_to: %s\n", grant.GrantedTo)
	fmt.Fprintf(&b, "granted_by: %s\n", firstNonEmpty(grant.GrantedBy, "-"))
	fmt.Fprintf(&b, "allowed_actions: %s\n", strings.Join(grant.AllowedActions, ","))
	if grant.AnchorFingerprint != "" {
		fmt.Fprintf(&b, "anchor_fingerprint: %s\n", grant.AnchorFingerprint)
	}
	if grant.StaleReason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", grant.StaleReason)
	}
	if !grant.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "expires_at: %s\n", grant.ExpiresAt.Format(time.RFC3339Nano))
	}
	if grant.InvocationCount > 0 || grant.FailureCount > 0 {
		fmt.Fprintf(&b, "counters: invocations=%d failures=%d\n", grant.InvocationCount, grant.FailureCount)
	}
	renderCapabilityChildRuntime(&b, grant.Contract, grant.Constraints)
	if update != nil {
		b.WriteString("capability_update_plan: present\n")
		fmt.Fprintf(&b, "policy_update_applied: %t\n", update.PolicyUpdateApplied)
		fmt.Fprintf(&b, "policy_changed: %t\n", update.PolicyChanged)
		fmt.Fprintf(&b, "policy_agent_id: %s\n", update.AgentID)
		fmt.Fprintf(&b, "policy_version: %d\n", update.PolicyVersion)
		fmt.Fprintf(&b, "policy_hash: %s\n", update.PolicyHash)
		if update.PolicyUpdateID > 0 {
			fmt.Fprintf(&b, "policy_update_id: %d\n", update.PolicyUpdateID)
		}
	}
	return b.String()
}

func renderCapabilityGrantList(records []session.CapabilityGrant) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_GRANTS]\n")
	fmt.Fprintf(&b, "count: %d\n", len(records))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeCapabilityGrant(record)
		fmt.Fprintf(&b, "- grant_id=%s kind=%s target_resource=%s status=%s granted_to=%s actions=%s\n", record.GrantID, record.Kind, firstNonEmpty(record.TargetResource, "-"), record.Status, firstNonEmpty(record.GrantedTo, "-"), strings.Join(record.AllowedActions, ","))
	}
	return b.String()
}

func renderCapabilityAccess(kind session.CapabilityKind, target string, principalID string, action string, allowed bool, grant session.CapabilityGrant) string {
	var b strings.Builder
	b.WriteString("[CAPABILITY_ACCESS]\n")
	fmt.Fprintf(&b, "kind: %s\n", kind)
	fmt.Fprintf(&b, "target_resource: %s\n", target)
	fmt.Fprintf(&b, "principal: %s\n", principalID)
	fmt.Fprintf(&b, "action: %s\n", action)
	fmt.Fprintf(&b, "allowed: %t\n", allowed)
	if allowed {
		fmt.Fprintf(&b, "grant_id: %s\n", grant.GrantID)
		fmt.Fprintf(&b, "grant_status: %s\n", grant.Status)
	}
	return b.String()
}
