//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) queueCapabilityRequestReviewEvent(record session.CapabilityRequest, in capabilityInput, actor principal.Principal, key session.SessionKey, target capabilityReviewTarget) (int64, error) {
	if r == nil || r.store == nil {
		return 0, fmt.Errorf("capability_request review notification requires transcript store")
	}
	record = session.NormalizeCapabilityRequest(record)
	if target.ChatID == 0 {
		return 0, nil
	}
	target.Scope = session.NormalizeScopeRef(target.Scope)
	if target.Scope.IsZero() {
		target.Scope = session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: fmt.Sprintf("%d", target.ChatID)}
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
	if contract := strings.TrimSpace(record.Contract); contract != "" {
		metadata["contract"] = contract
	}
	if constraints := strings.TrimSpace(record.Constraints); constraints != "" {
		metadata["constraints"] = constraints
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
	eventID, err := r.store.EnsurePendingReviewEvent(session.ReviewEvent{
		SourceChatID:      key.ChatID,
		SourceUserID:      actor.TelegramUserID,
		SourceRole:        "capability_request",
		SourceScope:       sourceScope,
		TargetAdminChatID: target.ChatID,
		TargetScope:       target.Scope,
		Summary:           summary,
		MetadataJSON:      string(raw),
		IdempotencyKey:    capabilityRequestReviewEventIdempotencyKey(target, record.RequestID),
	})
	if err != nil {
		return 0, err
	}
	if _, err := r.store.DismissPendingCapabilityReviewEvents(in.ReviewTargetChatID, record.RequestID, eventID); err != nil {
		return 0, err
	}
	return eventID, nil
}

func capabilityRequestReviewEventIdempotencyKey(target capabilityReviewTarget, requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if target.ChatID == 0 || requestID == "" {
		return ""
	}
	scope := session.NormalizeScopeRef(target.Scope)
	parts := []string{
		"capability_request_review",
		fmt.Sprintf("chat:%d", target.ChatID),
		"scope_kind:" + string(scope.Kind),
		"scope_id:" + scope.ID,
		"durable_agent:" + scope.DurableAgentID,
		"request:" + requestID,
	}
	return strings.Join(parts, "|")
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
