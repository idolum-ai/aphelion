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

func (r *Registry) toolAuthority(_ context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if p.Role != principal.RoleAdmin {
		return "", fmt.Errorf("tool_authority is admin-only")
	}
	if r.store == nil {
		return "", fmt.Errorf("tool_authority requires transcript store")
	}

	var in toolAuthorityInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode tool_authority input: %w", err)
		}
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "":
		return renderToolAuthorityHelp(), nil
	case "proposal_submit":
		return r.toolAuthorityProposalSubmit(in, p, key)
	case "proposal_show":
		return r.toolAuthorityProposalShow(in)
	case "proposal_list":
		return r.toolAuthorityProposalList(in)
	case "proposal_review":
		return r.toolAuthorityProposalReview(in, p, key)
	case "register":
		return r.toolAuthorityRegister(in, p, key)
	case "registered_show":
		return r.toolAuthorityRegisteredShow(in)
	case "registered_list":
		return r.toolAuthorityRegisteredList(in)
	case "exposure_set":
		return r.toolAuthorityExposureSet(in, p, key)
	case "exposure_show":
		return r.toolAuthorityExposureShow(in)
	case "exposure_list":
		return r.toolAuthorityExposureList(in)
	case "access_check":
		return r.toolAuthorityAccessCheck(in)
	default:
		return "", fmt.Errorf("tool_authority action %q is not supported", action)
	}
}

func (r *Registry) toolAuthorityProposalSubmit(in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	proposalID := strings.TrimSpace(in.ProposalID)
	if proposalID == "" {
		proposalID = generatedOperationID("tp")
	}
	status := session.NormalizeToolProposalReviewStatus(session.ToolProposalReviewStatus(in.ReviewStatus))
	if strings.TrimSpace(in.ReviewStatus) != "" && status == "" {
		return "", fmt.Errorf("tool_authority review_status must be proposed, approved, or rejected")
	}
	if status == "" {
		status = session.ToolProposalReviewStatusProposed
	}
	contract, err := normalizeContractBlob(in.Contract)
	if err != nil {
		return "", err
	}
	record, err := r.store.UpsertToolProposal(session.ToolProposal{
		ProposalID:       proposalID,
		ProposedBy:       strings.TrimSpace(in.ProposedBy),
		ToolName:         strings.TrimSpace(in.ToolName),
		WhyNow:           strings.TrimSpace(in.WhyNow),
		Contract:         contract,
		ReviewStatus:     status,
		RegisteredToolID: strings.TrimSpace(in.RegisteredToolID),
	})
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(
		key,
		core.ExecutionEventToolProposalCreated,
		string(record.ReviewStatus),
		map[string]any{
			"proposal_id":   record.ProposalID,
			"tool_name":     record.ToolName,
			"review_status": string(record.ReviewStatus),
			"proposed_by":   record.ProposedBy,
			"actor_role":    strings.TrimSpace(string(actor.Role)),
			"actor_user_id": actor.TelegramUserID,
		},
	); err != nil {
		return "", err
	}
	return renderToolProposal("[TOOL_PROPOSAL]", record), nil
}

func (r *Registry) toolAuthorityProposalShow(in toolAuthorityInput) (string, error) {
	proposalID := strings.TrimSpace(in.ProposalID)
	if proposalID == "" {
		return "", fmt.Errorf("tool_authority proposal_show requires proposal_id")
	}
	record, ok, err := r.store.ToolProposal(proposalID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool proposal %q not found", proposalID)
	}
	return renderToolProposal("[TOOL_PROPOSAL]", record), nil
}

func (r *Registry) toolAuthorityProposalList(in toolAuthorityInput) (string, error) {
	status := session.NormalizeToolProposalReviewStatus(session.ToolProposalReviewStatus(in.ReviewStatus))
	if strings.TrimSpace(in.ReviewStatus) != "" && status == "" {
		return "", fmt.Errorf("tool_authority review_status must be proposed, approved, or rejected")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	records, err := r.store.ToolProposals(limit, status)
	if err != nil {
		return "", err
	}
	return renderToolProposalList(records), nil
}

func (r *Registry) toolAuthorityProposalReview(in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	proposalID := strings.TrimSpace(in.ProposalID)
	if proposalID == "" {
		return "", fmt.Errorf("tool_authority proposal_review requires proposal_id")
	}
	status := session.NormalizeToolProposalReviewStatus(session.ToolProposalReviewStatus(in.ReviewStatus))
	if status == "" {
		return "", fmt.Errorf("tool_authority proposal_review requires review_status proposed, approved, or rejected")
	}
	record, ok, err := r.store.ToolProposal(proposalID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool proposal %q not found", proposalID)
	}
	record.ReviewStatus = status
	if id := strings.TrimSpace(in.RegisteredToolID); id != "" {
		record.RegisteredToolID = id
	}
	record, err = r.store.UpsertToolProposal(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(
		key,
		core.ExecutionEventToolProposalReviewed,
		string(record.ReviewStatus),
		map[string]any{
			"proposal_id":         record.ProposalID,
			"tool_name":           record.ToolName,
			"review_status":       string(record.ReviewStatus),
			"registered_tool_id":  record.RegisteredToolID,
			"actor_role":          strings.TrimSpace(string(actor.Role)),
			"actor_user_id":       actor.TelegramUserID,
			"requested_status":    strings.TrimSpace(in.ReviewStatus),
			"requested_tool_name": strings.TrimSpace(in.ToolName),
		},
	); err != nil {
		return "", err
	}
	return renderToolProposal("[TOOL_PROPOSAL_UPDATED]", record), nil
}

func (r *Registry) toolAuthorityRegister(in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	var proposal session.ToolProposal
	if proposalID := strings.TrimSpace(in.ProposalID); proposalID != "" {
		var ok bool
		var err error
		proposal, ok, err = r.store.ToolProposal(proposalID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("tool proposal %q not found", proposalID)
		}
		if proposal.ReviewStatus != session.ToolProposalReviewStatusApproved {
			return "", fmt.Errorf("tool proposal %q must be approved before registration", proposalID)
		}
	}

	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		toolName = strings.TrimSpace(proposal.ToolName)
	}
	if toolName == "" {
		return "", fmt.Errorf("tool_authority register requires tool_name")
	}
	implementationRef := strings.TrimSpace(in.ImplementationRef)
	if implementationRef == "" {
		return "", fmt.Errorf("tool_authority register requires implementation_ref")
	}
	registered := true
	if in.Registered != nil {
		registered = *in.Registered
	}
	record, err := r.store.UpsertRegisteredTool(session.RegisteredTool{
		ToolName:          toolName,
		ImplementationRef: implementationRef,
		Registered:        registered,
	})
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(proposal.ProposalID) != "" {
		proposal.RegisteredToolID = record.ToolName
		if _, err := r.store.UpsertToolProposal(proposal); err != nil {
			return "", err
		}
	}
	if err := r.appendToolAuthorityEvent(
		key,
		core.ExecutionEventToolRegistered,
		boolToStatus(record.Registered),
		map[string]any{
			"tool_name":           record.ToolName,
			"registered":          record.Registered,
			"implementation_ref":  record.ImplementationRef,
			"proposal_id":         strings.TrimSpace(proposal.ProposalID),
			"actor_role":          strings.TrimSpace(string(actor.Role)),
			"actor_user_id":       actor.TelegramUserID,
			"requested_tool_name": strings.TrimSpace(in.ToolName),
		},
	); err != nil {
		return "", err
	}
	return renderRegisteredTool("[REGISTERED_TOOL]", record), nil
}

func (r *Registry) toolAuthorityRegisteredShow(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority registered_show requires tool_name")
	}
	record, ok, err := r.store.RegisteredTool(toolName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("registered tool %q not found", toolName)
	}
	return renderRegisteredTool("[REGISTERED_TOOL]", record), nil
}

func (r *Registry) toolAuthorityRegisteredList(in toolAuthorityInput) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	records, err := r.store.RegisteredTools(limit)
	if err != nil {
		return "", err
	}
	return renderRegisteredToolList(records), nil
}

func (r *Registry) toolAuthorityExposureSet(in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	principalID := strings.TrimSpace(in.Principal)
	if toolName == "" || principalID == "" {
		return "", fmt.Errorf("tool_authority exposure_set requires tool_name and principal")
	}
	registered, ok, err := r.store.RegisteredTool(toolName)
	if err != nil {
		return "", err
	}
	if !ok || !registered.Registered {
		return "", fmt.Errorf("tool %q is not registered", toolName)
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	record, err := r.store.UpsertToolExposure(session.ToolExposure{
		ToolName:  toolName,
		Principal: principalID,
		Active:    active,
	})
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(
		key,
		core.ExecutionEventToolExposureChanged,
		boolToStatus(record.Active),
		map[string]any{
			"tool_name":     record.ToolName,
			"principal":     record.Principal,
			"active":        record.Active,
			"actor_role":    strings.TrimSpace(string(actor.Role)),
			"actor_user_id": actor.TelegramUserID,
		},
	); err != nil {
		return "", err
	}
	return renderToolExposure("[TOOL_EXPOSURE]", record), nil
}

func (r *Registry) toolAuthorityExposureShow(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	principalID := strings.TrimSpace(in.Principal)
	if toolName == "" || principalID == "" {
		return "", fmt.Errorf("tool_authority exposure_show requires tool_name and principal")
	}
	record, ok, err := r.store.ToolExposure(toolName, principalID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool exposure %q/%q not found", toolName, principalID)
	}
	return renderToolExposure("[TOOL_EXPOSURE]", record), nil
}

func (r *Registry) toolAuthorityExposureList(in toolAuthorityInput) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	records, err := r.store.ToolExposures(strings.TrimSpace(in.ToolName), strings.TrimSpace(in.Principal), limit)
	if err != nil {
		return "", err
	}
	return renderToolExposureList(records), nil
}

func (r *Registry) toolAuthorityAccessCheck(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	principalID := strings.TrimSpace(in.Principal)
	if toolName == "" || principalID == "" {
		return "", fmt.Errorf("tool_authority access_check requires tool_name and principal")
	}
	registered, registeredOK, err := r.store.RegisteredTool(toolName)
	if err != nil {
		return "", err
	}
	exposure, exposureOK, err := r.store.ToolExposure(toolName, principalID)
	if err != nil {
		return "", err
	}
	allowed := registeredOK && registered.Registered && exposureOK && exposure.Active
	var b strings.Builder
	b.WriteString("[TOOL_ACCESS]\n")
	fmt.Fprintf(&b, "tool_name: %s\n", toolName)
	fmt.Fprintf(&b, "principal: %s\n", principalID)
	fmt.Fprintf(&b, "registered: %t\n", registeredOK && registered.Registered)
	fmt.Fprintf(&b, "exposed_active: %t\n", exposureOK && exposure.Active)
	fmt.Fprintf(&b, "allowed: %t\n", allowed)
	return b.String(), nil
}

func normalizeContractBlob(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}", nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("tool_authority contract must be valid json")
	}
	return trimmed, nil
}

func (r *Registry) appendToolAuthorityEvent(key session.SessionKey, eventType string, status string, payload map[string]any) error {
	if r == nil || r.store == nil {
		return nil
	}
	payloadJSON := "{}"
	if len(payload) > 0 {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal tool authority event payload: %w", err)
		}
		payloadJSON = string(encoded)
	}
	_, err := r.store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   strings.TrimSpace(eventType),
		Stage:       "tool_authority",
		Status:      strings.TrimSpace(status),
		PayloadJSON: payloadJSON,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("append tool authority event %q: %w", strings.TrimSpace(eventType), err)
	}
	return nil
}

func boolToStatus(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

func renderToolAuthorityHelp() string {
	return strings.Join([]string{
		"[TOOL_AUTHORITY]",
		"actions:",
		"- proposal_submit | proposal_show | proposal_list | proposal_review",
		"- register | registered_show | registered_list",
		"- exposure_set | exposure_show | exposure_list",
		"- access_check",
	}, "\n")
}

func renderToolProposal(header string, record session.ToolProposal) string {
	record = session.NormalizeToolProposal(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "proposal_id: %s\n", record.ProposalID)
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "review_status: %s\n", record.ReviewStatus)
	if record.ProposedBy != "" {
		fmt.Fprintf(&b, "proposed_by: %s\n", record.ProposedBy)
	}
	if record.WhyNow != "" {
		fmt.Fprintf(&b, "why_now: %s\n", record.WhyNow)
	}
	if record.RegisteredToolID != "" {
		fmt.Fprintf(&b, "registered_tool_id: %s\n", record.RegisteredToolID)
	}
	if record.Contract != "" {
		fmt.Fprintf(&b, "contract: %s\n", record.Contract)
	}
	return b.String()
}

func renderToolProposalList(records []session.ToolProposal) string {
	var b strings.Builder
	b.WriteString("[TOOL_PROPOSALS]\n")
	b.WriteString(fmt.Sprintf("count: %d\n", len(records)))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolProposal(record)
		fmt.Fprintf(
			&b,
			"- proposal_id=%s tool_name=%s review_status=%s proposed_by=%s\n",
			record.ProposalID,
			record.ToolName,
			record.ReviewStatus,
			firstNonEmpty(record.ProposedBy, "-"),
		)
	}
	return b.String()
}

func renderRegisteredTool(header string, record session.RegisteredTool) string {
	record = session.NormalizeRegisteredTool(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "registered: %t\n", record.Registered)
	if record.ImplementationRef != "" {
		fmt.Fprintf(&b, "implementation_ref: %s\n", record.ImplementationRef)
	}
	return b.String()
}

func renderRegisteredToolList(records []session.RegisteredTool) string {
	var b strings.Builder
	b.WriteString("[REGISTERED_TOOLS]\n")
	b.WriteString(fmt.Sprintf("count: %d\n", len(records)))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeRegisteredTool(record)
		fmt.Fprintf(
			&b,
			"- tool_name=%s registered=%t implementation_ref=%s\n",
			record.ToolName,
			record.Registered,
			firstNonEmpty(record.ImplementationRef, "-"),
		)
	}
	return b.String()
}

func renderToolExposure(header string, record session.ToolExposure) string {
	record = session.NormalizeToolExposure(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "principal: %s\n", record.Principal)
	fmt.Fprintf(&b, "active: %t\n", record.Active)
	return b.String()
}

func renderToolExposureList(records []session.ToolExposure) string {
	var b strings.Builder
	b.WriteString("[TOOL_EXPOSURES]\n")
	b.WriteString(fmt.Sprintf("count: %d\n", len(records)))
	if len(records) == 0 {
		b.WriteString("- (none)\n")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolExposure(record)
		fmt.Fprintf(
			&b,
			"- tool_name=%s principal=%s active=%t\n",
			record.ToolName,
			record.Principal,
			record.Active,
		)
	}
	return b.String()
}
