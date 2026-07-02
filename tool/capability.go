//go:build linux

package tool

import (
	"context"
	"encoding/json"
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
		return r.capabilityAuthorityRequestReview(ctx, in, p, key)
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
	if err := validateCapabilityToolInvocationScopeJSON(contract, constraints); err != nil {
		return "", err
	}
	if err := validateCapabilityRemoteHostScopeJSON(contract, constraints); err != nil {
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
	if err := r.recordCapabilityRequestGrantHandoff(key, record, in, time.Now().UTC()); err != nil {
		return "", err
	}
	reviewTarget := capabilityRequestReviewTarget(in, key)
	reviewEventID := int64(0)
	if reviewTarget.ChatID != 0 {
		in.ReviewTargetChatID = reviewTarget.ChatID
		reviewEventID, err = r.queueCapabilityRequestReviewEvent(record, in, actor, key, reviewTarget)
		if err != nil {
			return "", err
		}
	}
	return renderCapabilityRequestWithReviewEvent("[CAPABILITY_REQUEST]", record, reviewEventID), nil
}

func (r *Registry) recordCapabilityRequestGrantHandoff(key session.SessionKey, record session.CapabilityRequest, in capabilityInput, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	requirement, ok := r.capabilityRequestGrantRequirement(record, in)
	if !ok {
		return nil
	}
	if !toolSessionKeyHasIdentity(key) {
		return fmt.Errorf("capability_request request_submit with grant actions requires session identity")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	operation, err := compileCapabilityGrantRecoveryHandoff(record, requirement)
	if err != nil {
		return fmt.Errorf("compile capability request grant handoff: %w", err)
	}
	projection := requirement.OperatorProjection
	if projection == "" {
		projection = fmt.Sprintf("Approve the exact %s grant for %s on %s.", requirement.Kind, requirement.GrantedTo, requirement.TargetResource)
	}
	_, err = r.store.RecordNextAction(session.NextActionInput{
		RecordID:           missingGrantNextActionRecordID(key, record.RequestID),
		Key:                key,
		Owner:              "capability_request",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "capability_request",
		SubjectRef:         record.RequestID,
		CausalRefs:         []string{"capability_request:" + record.RequestID, "capability:" + string(record.Kind) + ":" + record.TargetResource},
		NextAction:         "approve and activate the exact compiled capability grant",
		RequiredAuthority:  "capability_grant",
		ResourceBlocker:    "missing_capability_grant",
		RetryPolicy:        "retry_after_grant",
		OperationKind:      operation.Kind,
		OperationTool:      operation.Tool,
		OperationInputJSON: operation.InputJSON,
		OperatorProjection: projection,
		CreatedAt:          now,
	})
	if err != nil {
		return fmt.Errorf("record capability request grant handoff: %w", err)
	}
	return nil
}

func (r *Registry) capabilityRequestGrantRequirement(record session.CapabilityRequest, in capabilityInput) (missingGrantRequirement, bool) {
	record = session.NormalizeCapabilityRequest(record)
	actions := session.NormalizeCapabilityActions(in.AllowedActions)
	if len(actions) == 0 {
		if action := strings.TrimSpace(in.CapabilityAction); action != "" {
			actions = session.NormalizeCapabilityActions([]string{action})
		}
	}
	if len(actions) == 0 {
		return missingGrantRequirement{}, false
	}
	var store *session.SQLiteStore
	if r != nil {
		store = r.store
	}
	grantedTo := canonicalDurableAgentPrincipalIfKnown(store, firstNonEmpty(strings.TrimSpace(in.Principal), record.RequestedFor, record.RequestedBy))
	if grantedTo == "" {
		return missingGrantRequirement{}, false
	}
	reviewSummary := firstNonEmpty(strings.TrimSpace(in.ReviewSummary), record.Purpose)
	return missingGrantRequirement{
		RequestID:          record.RequestID,
		GrantID:            strings.TrimSpace(in.GrantID),
		Kind:               record.Kind,
		TargetResource:     record.TargetResource,
		GrantedTo:          grantedTo,
		AllowedActions:     actions,
		Contract:           record.Contract,
		Constraints:        record.Constraints,
		Purpose:            record.Purpose,
		RiskClass:          record.RiskClass,
		ReviewSummary:      reviewSummary,
		OperatorProjection: fmt.Sprintf("Approval will activate %s on %s for %s with actions %s.", record.Kind, record.TargetResource, grantedTo, strings.Join(actions, ",")),
		OperationKind:      "capability_grant_review",
		OperationTool:      "capability_authority",
		ExpiresInSeconds:   in.ExpiresInSeconds,
	}, true
}

type capabilityReviewTarget struct {
	ChatID int64
	Scope  session.ScopeRef
}

func capabilityRequestReviewTarget(in capabilityInput, key session.SessionKey) capabilityReviewTarget {
	if in.ReviewTargetChatID != 0 {
		id := fmt.Sprintf("%d", in.ReviewTargetChatID)
		return capabilityReviewTarget{
			ChatID: in.ReviewTargetChatID,
			Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: id},
		}
	}
	if key.ChatID == 0 {
		return capabilityReviewTarget{}
	}
	scope := session.NormalizeScopeRef(key.Scope)
	switch scope.Kind {
	case session.ScopeKindTelegramDM, session.ScopeKindTelegramGroup, session.ScopeKindTelegramThread:
		if scope.ID == "" {
			scope.ID = fmt.Sprintf("%d", key.ChatID)
		}
		return capabilityReviewTarget{ChatID: key.ChatID, Scope: scope}
	default:
		return capabilityReviewTarget{}
	}
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

func (r *Registry) capabilityAuthorityRequestReview(ctx context.Context, in capabilityInput, actor principal.Principal, key session.SessionKey) (string, error) {
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
	out := renderCapabilityRequest("[CAPABILITY_REQUEST_REVIEWED]", updated)
	if status == session.CapabilityReviewStatusApproved && strings.TrimSpace(updated.GrantID) == "" {
		grantOut, ok, err := r.materializeApprovedCapabilityRequestGrant(ctx, key, updated, in, review.Reviewer)
		if err != nil {
			return out, err
		}
		if ok {
			out = strings.TrimSpace(out + "\n\n" + grantOut)
		}
	}
	return out, nil
}

func (r *Registry) materializeApprovedCapabilityRequestGrant(ctx context.Context, key session.SessionKey, request session.CapabilityRequest, in capabilityInput, grantedBy string) (string, bool, error) {
	if r == nil || r.store == nil {
		return "", false, nil
	}
	request = session.NormalizeCapabilityRequest(request)
	grantedBy = strings.TrimSpace(grantedBy)
	records, err := r.store.OpenNextActionsBySubject("capability_request", request.RequestID, 100)
	if err != nil {
		return "", false, fmt.Errorf("load approved capability request grant handoffs: %w", err)
	}
	for _, record := range records {
		if strings.TrimSpace(record.OperationTool) != "capability_authority" ||
			strings.TrimSpace(record.OperationKind) != "capability_grant_review" ||
			strings.TrimSpace(record.OperationInputJSON) == "" {
			continue
		}
		var grantInput capabilityInput
		if err := json.Unmarshal([]byte(record.OperationInputJSON), &grantInput); err != nil {
			return "", false, fmt.Errorf("decode approved capability grant handoff %s: %w", strings.TrimSpace(record.RecordID), err)
		}
		if strings.ToLower(strings.TrimSpace(grantInput.Action)) != "grant_set" {
			continue
		}
		if strings.TrimSpace(grantInput.RequestID) == "" {
			grantInput.RequestID = request.RequestID
		}
		recordKey := sessionKeyForNextActionRecord(record)
		if !toolSessionKeyHasIdentity(recordKey) {
			recordKey = key
		}
		out, err := r.capabilityAuthorityGrantSetApproved(ctx, grantInput, recordKey, grantedBy)
		if err != nil {
			return "", false, err
		}
		return out, true, nil
	}
	if !capabilityReviewInputHasGrantTerms(in) {
		return "", false, nil
	}
	grantInput := in
	grantInput.RequestID = request.RequestID
	if strings.TrimSpace(grantInput.Kind) == "" {
		grantInput.Kind = string(request.Kind)
	}
	if strings.TrimSpace(grantInput.TargetResource) == "" {
		grantInput.TargetResource = request.TargetResource
	}
	if strings.TrimSpace(grantInput.Principal) == "" {
		grantInput.Principal = firstNonEmpty(request.RequestedFor, request.RequestedBy)
	}
	out, err := r.capabilityAuthorityGrantSetApproved(ctx, grantInput, key, grantedBy)
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

func capabilityReviewInputHasGrantTerms(in capabilityInput) bool {
	return strings.TrimSpace(in.GrantID) != "" ||
		strings.TrimSpace(in.Kind) != "" ||
		strings.TrimSpace(in.TargetResource) != "" ||
		strings.TrimSpace(in.Principal) != "" ||
		strings.TrimSpace(in.CapabilityAction) != "" ||
		len(session.NormalizeCapabilityActions(in.AllowedActions)) > 0 ||
		len(in.Contract) > 0 ||
		len(in.Constraints) > 0 ||
		in.ExpiresInSeconds > 0
}

func (r *Registry) capabilityAuthorityGrantSet(ctx context.Context, in capabilityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	if actor.Role != principal.RoleAdmin {
		return "", fmt.Errorf("capability_authority grant_set is admin-only")
	}
	return r.capabilityAuthorityGrantSetApproved(ctx, in, key, toolAuthorityPrincipalDisplay(actor))
}

func (r *Registry) capabilityAuthorityGrantSetApproved(ctx context.Context, in capabilityInput, key session.SessionKey, grantedBy string) (string, error) {
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
	grantedBy = strings.TrimSpace(grantedBy)
	if grantedBy == "" {
		grantedBy = "capability_authority"
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
	if err := validateCapabilityToolInvocationScopeJSON(contract, constraints); err != nil {
		return "", err
	}
	if err := validateCapabilityRemoteHostScopeJSON(contract, constraints); err != nil {
		return "", err
	}
	status := session.NormalizeCapabilityGrantStatus(session.CapabilityGrantStatus(in.GrantStatus))
	if strings.TrimSpace(in.GrantStatus) != "" && status == "" {
		return "", fmt.Errorf("capability_authority grant_status must be pending, active, stale, revoked, expired, or failed")
	}
	if status == "" {
		status = session.CapabilityGrantStatusActive
	}
	if err := r.validateCapabilityGrantTarget(grantedTo, status); err != nil {
		return "", err
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
		GrantedBy:          grantedBy,
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
			} else {
				warnDroppedEvidenceWrite("capability.grant.failure_status", storeErr)
			}
			if eventErr := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityGrantChanged, string(session.CapabilityGrantStatusFailed), map[string]any{
				"grant_id":        grant.GrantID,
				"request_id":      grant.RequestID,
				"kind":            string(grant.Kind),
				"target_resource": grant.TargetResource,
				"granted_to":      grant.GrantedTo,
				"status":          string(session.CapabilityGrantStatusFailed),
				"failure_reason":  failed.StaleReason,
			}); eventErr != nil {
				warnDroppedEvidenceWrite("capability.grant.failure_event", eventErr)
			}
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
	if grant.Status == session.CapabilityGrantStatusActive {
		if err := r.resolveCapabilityGrantRequestNextAction(key, grant, now); err != nil {
			return "", err
		}
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
		r.notifyCapabilityGrantObserver(key, grant)
	}
	return renderCapabilityGrantWithUpdate("[CAPABILITY_GRANT]", grant, updateResult), nil
}

func (r *Registry) resolveCapabilityGrantRequestNextAction(key session.SessionKey, grant session.CapabilityGrant, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	requestID := strings.TrimSpace(grant.RequestID)
	if requestID == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	records, err := r.store.OpenNextActionsBySubject("capability_request", requestID, 100)
	if err != nil {
		return fmt.Errorf("load capability request next actions after grant activation: %w", err)
	}
	if len(records) == 0 && toolSessionKeyHasIdentity(key) {
		return r.resolveCapabilityRequestNextActionForKey(key, requestID, now)
	}
	for _, record := range records {
		recordKey := sessionKeyForNextActionRecord(record)
		if !toolSessionKeyHasIdentity(recordKey) {
			continue
		}
		if err := r.resolveCapabilityRequestNextActionForKey(recordKey, requestID, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) resolveCapabilityRequestNextActionForKey(key session.SessionKey, requestID string, now time.Time) error {
	if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
		Key:         key,
		Owner:       "capability_authority",
		SubjectKind: "capability_request",
		SubjectRef:  strings.TrimSpace(requestID),
		Reason:      "capability_grant_active",
		ResolvedAt:  now,
	}); err != nil {
		return fmt.Errorf("resolve capability request next action after grant activation: %w", err)
	}
	return nil
}

func sessionKeyForNextActionRecord(record session.NextActionRecord) session.SessionKey {
	return session.SessionKey{
		ChatID: record.ChatID,
		UserID: record.UserID,
		Scope:  record.Scope,
	}
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
