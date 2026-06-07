//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

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
	eventID, err := r.store.InsertReviewEvent(session.ReviewEvent{
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
	if err != nil {
		return 0, err
	}
	if _, err := r.store.DismissPendingCapabilityReviewEvents(in.ReviewTargetChatID, record.RequestID, eventID); err != nil {
		return 0, err
	}
	return eventID, nil
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
