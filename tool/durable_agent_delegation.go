//go:build linux

package tool

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) requestDurableAgentDelegation(in durableAgentInput, actor principal.Principal, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for delegation_request")
	}
	if in.DelegationRequest == nil {
		return "", fmt.Errorf("durable_agent delegation_request requires delegation_request payload")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	payload := in.DelegationRequest
	reviewTarget := durableAgentReviewTargetChatID(*agent, payload.ReviewTargetChatID, in.ReviewTargetChatID)
	if reviewTarget == 0 {
		return "", fmt.Errorf("durable_agent delegation_request requires review_target_chat_id on the agent or payload")
	}
	agent.ReviewTargetChatID = reviewTarget

	requestID := strings.TrimSpace(payload.RequestID)
	if requestID == "" {
		requestID = generatedOperationID("cap")
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(payload.Kind))
	if strings.TrimSpace(payload.Kind) != "" && kind == "" {
		return "", fmt.Errorf("durable_agent delegation_request kind is not supported")
	}
	if kind == "" {
		kind = session.CapabilityKindGenericDelegation
	}
	target := strings.TrimSpace(payload.TargetResource)
	if target == "" {
		return "", fmt.Errorf("durable_agent delegation_request requires target_resource")
	}
	purpose := strings.TrimSpace(payload.Purpose)
	if purpose == "" {
		return "", fmt.Errorf("durable_agent delegation_request requires purpose")
	}
	contract, err := normalizeCapabilityJSONBlob(payload.Contract, "contract")
	if err != nil {
		return "", err
	}
	contract, err = mergeCapabilityUpdatePlanIntoContract(contract, capabilityUpdatePlanFromDurableDelegation(agent.AgentID, *payload))
	if err != nil {
		return "", err
	}
	constraints, err := normalizeCapabilityJSONBlob(payload.Constraints, "constraints")
	if err != nil {
		return "", err
	}
	requestedBy := canonicalDurableAgentPrincipalIfKnown(r.store, firstNonEmpty(payload.RequestedBy, core.DurableAgentPrincipal(agent.AgentID)))
	requestedFor := canonicalDurableAgentPrincipalIfKnown(r.store, firstNonEmpty(payload.RequestedFor, core.DurableAgentPrincipal(agent.AgentID)))
	parentPrincipal := firstNonEmpty(payload.ParentPrincipal, durableAgentDefaultParentPrincipal(*agent))
	adminPrincipal := firstNonEmpty(payload.AdminPrincipal, toolAuthorityPrincipalDisplay(actor))
	record, err := r.store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:       requestID,
		RequestedBy:     requestedBy,
		RequestedFor:    requestedFor,
		ParentPrincipal: parentPrincipal,
		AdminPrincipal:  adminPrincipal,
		Kind:            kind,
		TargetResource:  target,
		Purpose:         purpose,
		RiskClass:       strings.TrimSpace(payload.RiskClass),
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
		"request_via":       "durable_agent.delegation_request",
		"agent_id":          agent.AgentID,
		"channel_kind":      agent.ChannelKind,
	}); err != nil {
		return "", err
	}
	reviewEventID, err := durableagent.NewRuntime(r.store).QueueReviewArtifact(*agent, durableAgentDelegationRequestArtifact(*agent, record, *payload))
	if err != nil {
		return "", err
	}
	agreement, err := r.store.UpsertDurableChildAgreement(session.DurableChildAgreement{
		AgreementID:         "agreement-" + record.RequestID,
		AgentID:             agent.AgentID,
		ParentPrincipal:     parentPrincipal,
		ChildPrincipal:      requestedFor,
		SourceSurface:       "durable_agent.delegation_request",
		SourceRequestID:     record.RequestID,
		SourceReviewEventID: reviewEventID,
		Summary:             firstNonEmpty(strings.TrimSpace(payload.Summary), purpose),
		BoundedEffect:       durableAgentDelegationAgreementBoundedEffect(record, *payload),
		Status:              session.DurableChildAgreementStatusProposed,
		ArtifactRefs:        durableAgentDelegationAgreementArtifactRefs(reviewEventID, payload.ArtifactRefs),
	})
	if err != nil {
		return "", err
	}
	return renderDurableAgentDelegationRequest(*agent, record, reviewEventID, agreement), nil
}

func (r *Registry) reportDurableAgentDelegation(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for delegation_report")
	}
	if in.DelegationReport == nil {
		return "", fmt.Errorf("durable_agent delegation_report requires delegation_report payload")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	payload := in.DelegationReport
	reviewTarget := durableAgentReviewTargetChatID(*agent, payload.ReviewTargetChatID, in.ReviewTargetChatID)
	if reviewTarget == 0 {
		return "", fmt.Errorf("durable_agent delegation_report requires review_target_chat_id on the agent or payload")
	}
	agent.ReviewTargetChatID = reviewTarget
	if strings.TrimSpace(payload.Summary) == "" &&
		strings.TrimSpace(payload.Outcome) == "" &&
		len(payload.LocalActions) == 0 &&
		len(payload.Questions) == 0 &&
		len(payload.RiskFlags) == 0 {
		return "", fmt.Errorf("durable_agent delegation_report requires summary, outcome, local_actions, questions, or risk_flags")
	}
	if requestID := strings.TrimSpace(payload.RequestID); requestID != "" {
		if _, ok, err := r.store.CapabilityRequest(requestID); err != nil {
			return "", err
		} else if !ok {
			return "", fmt.Errorf("capability request %q not found", requestID)
		}
	}
	if grantID := strings.TrimSpace(payload.GrantID); grantID != "" {
		if _, ok, err := r.store.CapabilityGrant(grantID); err != nil {
			return "", err
		} else if !ok {
			return "", fmt.Errorf("capability grant %q not found", grantID)
		}
	}
	reviewEventID, err := durableagent.NewRuntime(r.store).QueueReviewArtifact(*agent, durableAgentDelegationReportArtifact(*agent, *payload))
	if err != nil {
		return "", err
	}
	if err := r.appendCapabilityEvent(key, "capability.reported", strings.TrimSpace(payload.Status), map[string]any{
		"agent_id":        agent.AgentID,
		"request_id":      strings.TrimSpace(payload.RequestID),
		"grant_id":        strings.TrimSpace(payload.GrantID),
		"status":          strings.TrimSpace(payload.Status),
		"outcome":         strings.TrimSpace(payload.Outcome),
		"review_event_id": reviewEventID,
		"report_via":      "durable_agent.delegation_report",
	}); err != nil {
		return "", err
	}
	return renderDurableAgentDelegationReport(*agent, *payload, reviewEventID), nil
}

func durableAgentDelegationAgreementBoundedEffect(record session.CapabilityRequest, input durableAgentDelegationRequestInput) string {
	parts := make([]string, 0, 4)
	if strings.TrimSpace(input.UpdateReason) != "" {
		parts = append(parts, "update_reason="+strings.TrimSpace(input.UpdateReason))
	}
	if strings.TrimSpace(record.TargetResource) != "" {
		parts = append(parts, "target="+strings.TrimSpace(record.TargetResource))
	}
	if len(input.GrantActions) > 0 {
		parts = append(parts, "grant_actions="+strings.Join(normalizePolicyCapabilities(input.GrantActions), ","))
	}
	if strings.TrimSpace(record.Purpose) != "" {
		parts = append(parts, "purpose="+strings.TrimSpace(record.Purpose))
	}
	return strings.Join(parts, "; ")
}

func durableAgentDelegationAgreementArtifactRefs(reviewEventID int64, refs []string) []session.RecordReference {
	out := make([]session.RecordReference, 0, len(refs)+1)
	if reviewEventID > 0 {
		out = append(out, session.RecordReference{Kind: "review_event", Ref: fmt.Sprintf("%d", reviewEventID), Label: "delegation request"})
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		out = append(out, session.RecordReference{Kind: "artifact", Ref: ref})
	}
	return session.NormalizeRecordReferences(out)
}

func renderDurableAgentDelegationRequest(agent core.DurableAgent, record session.CapabilityRequest, reviewEventID int64, agreement session.DurableChildAgreement) string {
	record = session.NormalizeCapabilityRequest(record)
	var b strings.Builder
	b.WriteString("action: durable-agent delegation request\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "request_id: %s\n", record.RequestID)
	fmt.Fprintf(&b, "review_event_id: %d\n", reviewEventID)
	if strings.TrimSpace(agreement.AgreementID) != "" {
		fmt.Fprintf(&b, "agreement_id: %s\n", agreement.AgreementID)
		fmt.Fprintf(&b, "agreement_status: %s\n", agreement.Status)
	}
	b.WriteString("canonical_surface: capability_request\n")
	b.WriteString("agreement_surface: durable_child_agreement\n")
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
	if plan, ok, err := capabilityUpdatePlanFromContract(record.Contract); err == nil && ok {
		b.WriteString("capability_update_plan: present\n")
		if plan.AgentID != "" {
			fmt.Fprintf(&b, "policy_agent_id: %s\n", plan.AgentID)
		}
		if capabilityUpdatePlanHasDurablePolicyPatch(plan) {
			b.WriteString("policy_update_on_grant: true\n")
		}
	}
	b.WriteString("next: capability_authority request_review, then grant_set if approved\n")
	return b.String()
}

func renderDurableAgentDelegationReport(agent core.DurableAgent, input durableAgentDelegationReportInput, reviewEventID int64) string {
	var b strings.Builder
	b.WriteString("action: durable-agent delegation report\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "review_event_id: %d\n", reviewEventID)
	if requestID := strings.TrimSpace(input.RequestID); requestID != "" {
		fmt.Fprintf(&b, "request_id: %s\n", requestID)
	}
	if grantID := strings.TrimSpace(input.GrantID); grantID != "" {
		fmt.Fprintf(&b, "grant_id: %s\n", grantID)
	}
	if status := strings.TrimSpace(input.Status); status != "" {
		fmt.Fprintf(&b, "status: %s\n", status)
	}
	if outcome := strings.TrimSpace(input.Outcome); outcome != "" {
		fmt.Fprintf(&b, "outcome: %s\n", outcome)
	}
	b.WriteString("next: review queued artifact and update capability grant/request if needed\n")
	return b.String()
}

func durableAgentDelegationRequestArtifact(agent core.DurableAgent, record session.CapabilityRequest, input durableAgentDelegationRequestInput) core.DurableReviewArtifact {
	record = session.NormalizeCapabilityRequest(record)
	metadata := cloneDurableAgentDelegationMetadata(input.Metadata)
	putDurableAgentDelegationMetadata(metadata, "delegation_surface", "durable_agent.delegation_request")
	putDurableAgentDelegationMetadata(metadata, "capability_request_id", record.RequestID)
	putDurableAgentDelegationMetadata(metadata, "capability_kind", string(record.Kind))
	putDurableAgentDelegationMetadata(metadata, "target_resource", record.TargetResource)
	putDurableAgentDelegationMetadata(metadata, "requested_by", record.RequestedBy)
	putDurableAgentDelegationMetadata(metadata, "requested_for", record.RequestedFor)
	putDurableAgentDelegationMetadata(metadata, "review_status", string(record.ReviewStatus))
	putDurableAgentDelegationMetadata(metadata, "purpose", record.Purpose)
	if plan, ok, err := capabilityUpdatePlanFromContract(record.Contract); err == nil && ok {
		putDurableAgentDelegationMetadata(metadata, "capability_update_plan", "present")
		if plan.AgentID != "" {
			putDurableAgentDelegationMetadata(metadata, "policy_agent_id", plan.AgentID)
		}
		if capabilityUpdatePlanHasDurablePolicyPatch(plan) {
			putDurableAgentDelegationMetadata(metadata, "policy_update_on_grant", "true")
		}
	}

	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Delegation request %s: %s requests %s access to %s. Purpose: %s", record.RequestID, firstNonEmpty(record.RequestedFor, agent.AgentID), record.Kind, record.TargetResource, record.Purpose)
	}
	localActions := normalizeDurableAgentDelegationStrings(input.LocalActions)
	localActions = appendDurableAgentDelegationString(localActions, fmt.Sprintf("created capability_request %s with review_status %s", record.RequestID, record.ReviewStatus))
	questions := normalizeDurableAgentDelegationStrings(input.Questions)
	if len(questions) == 0 {
		questions = append(questions, fmt.Sprintf("Review capability_request %s and approve, reject, or grant allowed actions if acceptable.", record.RequestID))
	}
	riskFlags := normalizeDurableAgentDelegationStrings(input.RiskFlags)
	if record.RiskClass != "" {
		riskFlags = appendDurableAgentDelegationString(riskFlags, "risk_class:"+record.RiskClass)
	}
	return core.DurableReviewArtifact{
		AgentID:      agent.AgentID,
		Summary:      summary,
		LocalActions: localActions,
		Questions:    questions,
		RiskFlags:    riskFlags,
		ArtifactRefs: normalizeDurableAgentDelegationStrings(input.ArtifactRefs),
		Metadata:     metadata,
	}
}

func durableAgentDelegationReportArtifact(agent core.DurableAgent, input durableAgentDelegationReportInput) core.DurableReviewArtifact {
	metadata := cloneDurableAgentDelegationMetadata(input.Metadata)
	putDurableAgentDelegationMetadata(metadata, "delegation_surface", "durable_agent.delegation_report")
	putDurableAgentDelegationMetadata(metadata, "capability_request_id", input.RequestID)
	putDurableAgentDelegationMetadata(metadata, "capability_grant_id", input.GrantID)
	putDurableAgentDelegationMetadata(metadata, "status", input.Status)
	putDurableAgentDelegationMetadata(metadata, "outcome", input.Outcome)

	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Delegation report from %s: %s", strings.TrimSpace(agent.AgentID), firstNonEmpty(input.Outcome, input.Status, "needs review"))
	}
	return core.DurableReviewArtifact{
		AgentID:      agent.AgentID,
		Summary:      summary,
		LocalActions: normalizeDurableAgentDelegationStrings(input.LocalActions),
		Questions:    normalizeDurableAgentDelegationStrings(input.Questions),
		RiskFlags:    normalizeDurableAgentDelegationStrings(input.RiskFlags),
		ArtifactRefs: normalizeDurableAgentDelegationStrings(input.ArtifactRefs),
		Metadata:     metadata,
	}
}

func durableAgentReviewTargetChatID(agent core.DurableAgent, overrides ...int64) int64 {
	for _, value := range overrides {
		if value > 0 {
			return value
		}
	}
	return agent.ReviewTargetChatID
}

func durableAgentDefaultParentPrincipal(agent core.DurableAgent) string {
	kind := strings.TrimSpace(agent.ParentScopeKind)
	id := strings.TrimSpace(agent.ParentScopeID)
	if kind == "" || id == "" {
		return ""
	}
	switch session.ScopeKind(kind) {
	case session.ScopeKindTelegramDM:
		return "telegram:" + id
	case session.ScopeKindDurableAgent:
		return id
	default:
		return kind + ":" + id
	}
}

func cloneDurableAgentDelegationMetadata(input map[string]string) map[string]string {
	out := make(map[string]string, len(input)+8)
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func putDurableAgentDelegationMetadata(metadata map[string]string, key string, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	metadata[key] = value
}

func normalizeDurableAgentDelegationStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendDurableAgentDelegationString(out, value)
	}
	return out
}

func appendDurableAgentDelegationString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
