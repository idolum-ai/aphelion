//go:build linux

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) toolAuthority(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
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
	case "proposal_ratify":
		return r.toolAuthorityProposalRatify(ctx, in, p, key)
	case "proposal_override":
		return r.toolAuthorityProposalOverride(in, p, key)
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
	case "install_set":
		return r.toolAuthorityInstallSet(in, p, key)
	case "install_show":
		return r.toolAuthorityInstallShow(in)
	case "install_list":
		return r.toolAuthorityInstallList(in)
	case "install_execute":
		return r.toolAuthorityInstallExecute(ctx, in, p, key)
	case "audit_run":
		return r.toolAuthorityAuditRun(in, p, key)
	case "audit_show":
		return r.toolAuthorityAuditShow(in)
	case "audit_list":
		return r.toolAuthorityAuditList(in)
	case "probe_run":
		return r.toolAuthorityProbeRun(ctx, in, p, key)
	case "probe_show":
		return r.toolAuthorityProbeShow(in)
	case "probe_list":
		return r.toolAuthorityProbeList(in)
		return r.toolAuthorityProbeRun(ctx, in, p, key)
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
	if status != session.ToolProposalReviewStatusProposed {
		return "", fmt.Errorf("tool_authority proposal_submit only accepts review_status=proposed; use proposal_review, proposal_ratify, or proposal_override")
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
	if status == session.ToolProposalReviewStatusApproved {
		return "", fmt.Errorf("tool_authority proposal_review cannot set approved; use proposal_ratify or proposal_override")
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
			"review_via":          "direct_review",
			"requested_status":    strings.TrimSpace(in.ReviewStatus),
			"requested_tool_name": strings.TrimSpace(in.ToolName),
		},
	); err != nil {
		return "", err
	}
	return renderToolProposal("[TOOL_PROPOSAL_UPDATED]", record), nil
}

func (r *Registry) toolAuthorityProposalOverride(in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	proposalID := strings.TrimSpace(in.ProposalID)
	if proposalID == "" {
		return "", fmt.Errorf("tool_authority proposal_override requires proposal_id")
	}
	status := session.NormalizeToolProposalReviewStatus(session.ToolProposalReviewStatus(in.ReviewStatus))
	if status == "" {
		return "", fmt.Errorf("tool_authority proposal_override requires review_status proposed, approved, or rejected")
	}
	overrideReason := strings.TrimSpace(in.OverrideReason)
	if overrideReason == "" {
		return "", fmt.Errorf("tool_authority proposal_override requires override_reason")
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
			"review_via":          "override",
			"override_reason":     overrideReason,
			"requested_status":    strings.TrimSpace(in.ReviewStatus),
			"requested_tool_name": strings.TrimSpace(in.ToolName),
		},
	); err != nil {
		return "", err
	}
	return renderToolProposal("[TOOL_PROPOSAL_UPDATED]", record), nil
}

func (r *Registry) toolAuthorityProposalRatify(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	proposalID := strings.TrimSpace(in.ProposalID)
	if proposalID == "" {
		return "", fmt.Errorf("tool_authority proposal_ratify requires proposal_id")
	}
	if r.toolProposalRatificationApprover == nil {
		return "", fmt.Errorf("tool_authority proposal_ratify requires ratification approver")
	}
	record, ok, err := r.store.ToolProposal(proposalID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool proposal %q not found", proposalID)
	}
	if record.ReviewStatus != session.ToolProposalReviewStatusProposed {
		return "", fmt.Errorf(
			"tool proposal %q review_status=%s cannot be ratified; expected proposed",
			proposalID,
			record.ReviewStatus,
		)
	}

	decision, err := r.toolProposalRatificationApprover.ConfirmToolProposalRatification(ctx, ToolProposalRatificationApprovalRequest{
		Principal:  actor,
		SessionKey: key,
		Proposal:   record,
	})
	if err != nil {
		return "", err
	}

	transitionReason := "denied"
	record.ReviewStatus = session.ToolProposalReviewStatusRejected
	if decision.Approved {
		transitionReason = "approved"
		record.ReviewStatus = session.ToolProposalReviewStatusApproved
	} else if decision.TimedOut {
		transitionReason = "timed_out"
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
			"proposal_id":        record.ProposalID,
			"tool_name":          record.ToolName,
			"review_status":      string(record.ReviewStatus),
			"registered_tool_id": record.RegisteredToolID,
			"actor_role":         strings.TrimSpace(string(actor.Role)),
			"actor_user_id":      actor.TelegramUserID,
			"ratified_via":       "decision_broker",
			"transition_reason":  transitionReason,
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
	trustedToolName, ok := r.canonicalTrustedToolName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority register tool_name %q is not a known runtime tool definition", toolName)
	}
	if !r.authorityManagedTool(trustedToolName) {
		return "", fmt.Errorf("tool_authority register tool_name %q is not an authority-managed runtime tool", toolName)
	}
	toolName = trustedToolName
	if manifest, ok := r.externalManifestByName(toolName); ok {
		record, exists, err := r.store.ToolInstallRecord(toolName)
		if err != nil {
			return "", err
		}
		if !exists || record.Status != session.ToolInstallStatusVerified {
			return "", fmt.Errorf("external tool %q requires a verified install record before registration", manifest.Name)
		}
		audit, auditExists, err := r.store.ToolAuditRecord(toolName)
		if err != nil {
			return "", err
		}
		if !auditExists || audit.Status != session.ToolAuditStatusPassed {
			return "", fmt.Errorf("external tool %q requires a passed import audit before registration", manifest.Name)
		}
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

func (r *Registry) toolAuthorityInstallSet(in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority install_set requires tool_name")
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority install_set requires an external tool manifest-backed tool_name")
	}
	status := session.NormalizeToolInstallStatus(session.ToolInstallStatus(in.Status))
	if status == "" {
		return "", fmt.Errorf("tool_authority install_set requires status pending, installed, verified, failed, or stale")
	}
	probeStatus := session.NormalizeToolProbeStatus(session.ToolProbeStatus(in.ProbeStatus))
	if strings.TrimSpace(in.ProbeStatus) != "" && probeStatus == "" {
		return "", fmt.Errorf("tool_authority probe_status must be passed or failed")
	}
	now := time.Now().UTC()
	record, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	if !exists {
		record = session.ToolInstallRecord{ToolName: manifest.Name}
	}
	record.Installer = firstNonEmpty(strings.TrimSpace(in.Installer), record.Installer)
	record.InstallRef = firstNonEmpty(strings.TrimSpace(in.InstallRef), record.InstallRef)
	record.Status = status
	if strings.TrimSpace(in.ProbeOutput) != "" {
		record.ProbeOutput = strings.TrimSpace(in.ProbeOutput)
	}
	if probeStatus != "" {
		record.ProbeStatus = probeStatus
		record.LastProbedAt = now
		if _, err := r.store.UpsertToolProbeRecord(session.ToolProbeRecord{
			ToolName:    manifest.Name,
			Status:      probeStatus,
			ProbeOutput: firstNonEmpty(strings.TrimSpace(in.ProbeOutput), record.ProbeOutput),
			ProbedAt:    now,
		}); err != nil {
			return "", err
		}
	}
	switch status {
	case session.ToolInstallStatusInstalled:
		if record.InstalledAt.IsZero() {
			record.InstalledAt = now
		}
		if record.AttestedAt.IsZero() == false && record.ProbeStatus == session.ToolProbeStatusFailed {
			record.AttestedAt = time.Time{}
		}
	case session.ToolInstallStatusVerified:
		if record.InstalledAt.IsZero() {
			record.InstalledAt = now
		}
		probe, ok, err := r.store.ToolProbeRecord(manifest.Name)
		if err != nil {
			return "", err
		}
		if !ok || probe.Status != session.ToolProbeStatusPassed || probe.ProbedAt.IsZero() || (!record.InstalledAt.IsZero() && probe.ProbedAt.Before(record.InstalledAt)) {
			return "", fmt.Errorf("tool_authority install_set verified status requires probe_status=passed and a current probe record")
		}
		audit, ok, err := r.store.ToolAuditRecord(manifest.Name)
		if err != nil {
			return "", err
		}
		if !ok || audit.Status != session.ToolAuditStatusPassed || audit.AuditedAt.IsZero() || (!record.InstalledAt.IsZero() && audit.AuditedAt.Before(record.InstalledAt)) {
			return "", fmt.Errorf("tool_authority install_set verified status requires a passed import audit")
		}
		record.AttestedAt = now
	case session.ToolInstallStatusPending:
		record.AttestedAt = time.Time{}
	case session.ToolInstallStatusFailed:
		record.AttestedAt = time.Time{}
	case session.ToolInstallStatusStale:
		record.AttestedAt = time.Time{}
	}
	record.UpdatedAt = now
	stored, err := r.store.UpsertToolInstallRecord(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(
		key,
		core.ExecutionEventToolInstallUpdated,
		string(stored.Status),
		map[string]any{
			"tool_name":     toolName,
			"status":        string(stored.Status),
			"installer":     stored.Installer,
			"install_ref":   stored.InstallRef,
			"probe_status":  string(stored.ProbeStatus),
			"actor_role":    strings.TrimSpace(string(actor.Role)),
			"actor_user_id": actor.TelegramUserID,
		},
	); err != nil {
		return "", err
	}
	return renderToolInstallRecord("[TOOL_INSTALL]", stored), nil
}

func (r *Registry) toolAuthorityInstallShow(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority install_show requires tool_name")
	}
	record, ok, err := r.store.ToolInstallRecord(toolName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool install record %q not found", toolName)
	}
	return renderToolInstallRecord("[TOOL_INSTALL]", record), nil
}

func (r *Registry) toolAuthorityInstallList(in toolAuthorityInput) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	status := session.NormalizeToolInstallStatus(session.ToolInstallStatus(in.Status))
	if strings.TrimSpace(in.Status) != "" && status == "" {
		return "", fmt.Errorf("tool_authority install_list status must be pending, installed, verified, failed, or stale")
	}
	records, err := r.store.ToolInstallRecords(status, limit)
	if err != nil {
		return "", err
	}
	return renderToolInstallRecordList(records), nil
}

func (r *Registry) toolAuthorityInstallExecute(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority install_execute requires tool_name")
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority install_execute requires an external tool manifest-backed tool_name")
	}
	manifest = NormalizeExternalToolManifest(manifest)
	if len(manifest.Install.Command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare an install command", manifest.Name)
	}
	record, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("external tool %q requires an install record before install_execute", manifest.Name)
	}
	installOutput, err := r.runExternalManifestCommand(ctx, manifest, manifest.Install.Command)
	now := time.Now().UTC()
	record.ProbeOutput = strings.TrimSpace(installOutput)
	record.UpdatedAt = now
	if err != nil {
		record.Status = session.ToolInstallStatusFailed
		record.AttestedAt = time.Time{}
		stored, saveErr := r.store.UpsertToolInstallRecord(record)
		if saveErr != nil {
			return "", saveErr
		}
		_ = r.appendToolAuthorityEvent(key, core.ExecutionEventToolInstallUpdated, string(stored.Status), map[string]any{
			"tool_name":     stored.ToolName,
			"status":        string(stored.Status),
			"install_ref":   stored.InstallRef,
			"actor_role":    strings.TrimSpace(string(actor.Role)),
			"actor_user_id": actor.TelegramUserID,
		})
		return "", err
	}
	record.Status = session.ToolInstallStatusInstalled
	record.InstalledAt = now
	record.AttestedAt = time.Time{}
	stored, err := r.store.UpsertToolInstallRecord(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, core.ExecutionEventToolInstallUpdated, string(stored.Status), map[string]any{
		"tool_name":     stored.ToolName,
		"status":        string(stored.Status),
		"install_ref":   stored.InstallRef,
		"actor_role":    strings.TrimSpace(string(actor.Role)),
		"actor_user_id": actor.TelegramUserID,
	}); err != nil {
		return "", err
	}
	return renderToolInstallRecord("[TOOL_INSTALL]", stored), nil
}

func (r *Registry) toolAuthorityAuditRun(in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority audit_run requires tool_name")
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority audit_run requires an external tool manifest-backed tool_name")
	}
	if _, exists, err := r.store.ToolInstallRecord(manifest.Name); err != nil {
		return "", err
	} else if !exists {
		return "", fmt.Errorf("external tool %q requires an install record before audit_run", manifest.Name)
	}
	output, err := r.runExternalManifestAudit(manifest)
	now := time.Now().UTC()
	record := session.ToolAuditRecord{ToolName: manifest.Name, AuditOutput: output, UpdatedAt: now, AuditedAt: now}
	if err != nil {
		record.Status = session.ToolAuditStatusFailed
		stored, saveErr := r.store.UpsertToolAuditRecord(record)
		if saveErr != nil {
			return "", saveErr
		}
		_ = r.appendToolAuthorityEvent(key, core.ExecutionEventToolAuditUpdated, string(stored.Status), map[string]any{"tool_name": stored.ToolName, "status": string(stored.Status), "actor_role": strings.TrimSpace(string(actor.Role)), "actor_user_id": actor.TelegramUserID})
		return "", err
	}
	record.Status = session.ToolAuditStatusPassed
	stored, err := r.store.UpsertToolAuditRecord(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, core.ExecutionEventToolAuditUpdated, string(stored.Status), map[string]any{"tool_name": stored.ToolName, "status": string(stored.Status), "actor_role": strings.TrimSpace(string(actor.Role)), "actor_user_id": actor.TelegramUserID}); err != nil {
		return "", err
	}
	return renderToolAuditRecord("[TOOL_AUDIT]", stored), nil
}

func (r *Registry) toolAuthorityAuditShow(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority audit_show requires tool_name")
	}
	record, ok, err := r.store.ToolAuditRecord(toolName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool audit record %q not found", toolName)
	}
	return renderToolAuditRecord("[TOOL_AUDIT]", record), nil
}

func (r *Registry) toolAuthorityAuditList(in toolAuthorityInput) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	records, err := r.store.ToolAuditRecords("", limit)
	if err != nil {
		return "", err
	}
	return renderToolAuditRecordList(records), nil
}

func (r *Registry) runExternalManifestAudit(manifest ExternalToolManifest) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	workdir, err := resolveWorkdir(r.workspace, manifest.Execution.Workdir)
	if err != nil {
		return "", err
	}
	entry := strings.TrimSpace(manifest.Execution.Entry)
	if entry == "" {
		return "", fmt.Errorf("external tool %q execution entry is empty", manifest.Name)
	}
	firstToken := strings.Fields(entry)
	if len(firstToken) == 0 {
		return "", fmt.Errorf("external tool %q execution entry is empty", manifest.Name)
	}
	target := firstToken[0]
	if strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") {
		resolved := target
		if !strings.HasPrefix(target, "/") {
			resolved = filepath.Join(workdir, target)
		}
		if _, err := os.Stat(resolved); err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("entry_path: %s", resolved), fmt.Errorf("external tool %q import audit failed: entry path does not exist", manifest.Name)
			}
			return fmt.Sprintf("entry_path: %s", resolved), fmt.Errorf("external tool %q import audit stat failed: %w", manifest.Name, err)
		}
		return fmt.Sprintf("entry_path: %s", resolved), nil
	}
	return fmt.Sprintf("entry_command: %s", target), nil
}

func (r *Registry) toolAuthorityProbeRun(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority probe_run requires tool_name")
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority probe_run requires an external tool manifest-backed tool_name")
	}
	manifest = NormalizeExternalToolManifest(manifest)
	if len(manifest.Probe.Command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a probe command", manifest.Name)
	}
	record, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("external tool %q requires an install record before probe_run", manifest.Name)
	}
	probeOutput, err := r.runExternalManifestProbe(ctx, manifest)
	now := time.Now().UTC()
	record.ProbeOutput = probeOutput
	record.LastProbedAt = now
	if err != nil {
		record.ProbeStatus = session.ToolProbeStatusFailed
		if _, saveProbeErr := r.store.UpsertToolProbeRecord(session.ToolProbeRecord{ToolName: manifest.Name, Status: session.ToolProbeStatusFailed, ProbeOutput: probeOutput, ProbedAt: now}); saveProbeErr != nil {
			return "", saveProbeErr
		}
		if record.Status == session.ToolInstallStatusVerified {
			record.Status = session.ToolInstallStatusStale
		} else {
			record.Status = session.ToolInstallStatusFailed
		}
		record.AttestedAt = time.Time{}
		record.UpdatedAt = now
		stored, saveErr := r.store.UpsertToolInstallRecord(record)
		if saveErr != nil {
			return "", saveErr
		}
		_ = r.appendToolAuthorityEvent(key, core.ExecutionEventToolInstallUpdated, string(stored.Status), map[string]any{
			"tool_name":     stored.ToolName,
			"status":        string(stored.Status),
			"probe_status":  string(stored.ProbeStatus),
			"install_ref":   stored.InstallRef,
			"actor_role":    strings.TrimSpace(string(actor.Role)),
			"actor_user_id": actor.TelegramUserID,
		})
		return "", err
	}
	record.ProbeStatus = session.ToolProbeStatusPassed
	if _, err := r.store.UpsertToolProbeRecord(session.ToolProbeRecord{ToolName: manifest.Name, Status: session.ToolProbeStatusPassed, ProbeOutput: probeOutput, ProbedAt: now}); err != nil {
		return "", err
	}
	record.UpdatedAt = now
	stored, err := r.store.UpsertToolInstallRecord(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, core.ExecutionEventToolInstallUpdated, string(stored.Status), map[string]any{
		"tool_name":     stored.ToolName,
		"status":        string(stored.Status),
		"probe_status":  string(stored.ProbeStatus),
		"install_ref":   stored.InstallRef,
		"actor_role":    strings.TrimSpace(string(actor.Role)),
		"actor_user_id": actor.TelegramUserID,
	}); err != nil {
		return "", err
	}
	return renderToolInstallRecord("[TOOL_INSTALL]", stored), nil
}

func (r *Registry) runExternalManifestCommand(ctx context.Context, manifest ExternalToolManifest, command []string) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if len(command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a command", manifest.Name)
	}
	workdir, err := resolveWorkdir(r.workspace, manifest.Execution.Workdir)
	if err != nil {
		return "", err
	}
	timeout := defaultTimeout(15 * time.Second)
	if manifest.Execution.TimeoutSeconds > 0 {
		timeout = time.Duration(manifest.Execution.TimeoutSeconds) * time.Second
	}
	if manifest.Constraints.MaxRuntimeSeconds > 0 {
		constraintTimeout := time.Duration(manifest.Constraints.MaxRuntimeSeconds) * time.Second
		if timeout <= 0 || constraintTimeout < timeout {
			timeout = constraintTimeout
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
	cmd.Dir = workdir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	output := renderOutput(stdout.String(), stderr.String(), r.maxOutputBytes)
	if runErr != nil {
		return output, fmt.Errorf("external tool %q install execution failed: %s", manifest.Name, output)
	}
	return output, nil
}

func (r *Registry) runExternalManifestProbe(ctx context.Context, manifest ExternalToolManifest) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if len(manifest.Probe.Command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a probe command", manifest.Name)
	}
	output, runErr := r.runExternalManifestCommand(ctx, manifest, manifest.Probe.Command)
	if runErr != nil {
		return output, fmt.Errorf("external tool %q probe execution failed: %s", manifest.Name, output)
	}
	if expected := strings.TrimSpace(manifest.Probe.ExpectedOutputContains); expected != "" {
		if !strings.Contains(output, expected) {
			return output, fmt.Errorf("external tool %q probe output did not contain expected text %q", manifest.Name, expected)
		}
	}
	return output, nil
}

func (r *Registry) toolAuthorityProbeShow(in toolAuthorityInput) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority probe_show requires tool_name")
	}
	record, ok, err := r.store.ToolProbeRecord(toolName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool probe record %q not found", toolName)
	}
	return renderToolProbeRecord("[TOOL_PROBE]", record), nil
}

func (r *Registry) toolAuthorityProbeList(in toolAuthorityInput) (string, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	status := session.NormalizeToolProbeStatus(session.ToolProbeStatus(in.ProbeStatus))
	if strings.TrimSpace(in.ProbeStatus) != "" && status == "" {
		return "", fmt.Errorf("tool_authority probe_list probe_status must be passed or failed")
	}
	records, err := r.store.ToolProbeRecords(status, limit)
	if err != nil {
		return "", err
	}
	return renderToolProbeRecordList(records), nil
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

func (r *Registry) canonicalTrustedToolName(raw string) (string, bool) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", false
	}
	for _, def := range r.Definitions() {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		if strings.EqualFold(name, target) {
			return name, true
		}
	}
	if manifest, ok := r.externalManifestByName(target); ok {
		return strings.TrimSpace(manifest.Name), true
	}
	return "", false
}

func renderToolAuthorityHelp() string {
	return strings.Join([]string{
		"[TOOL_AUTHORITY]",
		"actions:",
		"- proposal_submit | proposal_show | proposal_list | proposal_review | proposal_ratify | proposal_override",
		"- register | registered_show | registered_list",
		"- exposure_set | exposure_show | exposure_list",
		"- install_set | install_show | install_list | install_execute | audit_run | audit_show | audit_list | probe_run | probe_show | probe_list",
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

func renderToolProbeRecord(header string, record session.ToolProbeRecord) string {
	record = session.NormalizeToolProbeRecord(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(string(record.Status), "-"))
	fmt.Fprintf(&b, "probe_output: %s\n", firstNonEmpty(record.ProbeOutput, "-"))
	if !record.ProbedAt.IsZero() {
		fmt.Fprintf(&b, "probed_at: %s\n", record.ProbedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", record.UpdatedAt.UTC().Format(time.RFC3339))
	return strings.TrimRight(b.String(), "\n")
}

func renderToolProbeRecordList(records []session.ToolProbeRecord) string {
	var b strings.Builder
	b.WriteString("[TOOL_PROBES]")
	if len(records) == 0 {
		b.WriteString("\n- (none)")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolProbeRecord(record)
		b.WriteString("\n- ")
		b.WriteString(record.ToolName)
		b.WriteString(" status=")
		b.WriteString(firstNonEmpty(string(record.Status), "-"))
	}
	return b.String()
}
func renderToolAuditRecord(header string, record session.ToolAuditRecord) string {
	record = session.NormalizeToolAuditRecord(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(string(record.Status), "-"))
	fmt.Fprintf(&b, "audit_output: %s\n", firstNonEmpty(record.AuditOutput, "-"))
	if !record.AuditedAt.IsZero() {
		fmt.Fprintf(&b, "audited_at: %s\n", record.AuditedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", record.UpdatedAt.UTC().Format(time.RFC3339))
	return strings.TrimRight(b.String(), "\n")
}

func renderToolAuditRecordList(records []session.ToolAuditRecord) string {
	var b strings.Builder
	b.WriteString("[TOOL_AUDITS]")
	if len(records) == 0 {
		b.WriteString("\n- (none)")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolAuditRecord(record)
		b.WriteString("\n- ")
		b.WriteString(record.ToolName)
		b.WriteString(" status=")
		b.WriteString(firstNonEmpty(string(record.Status), "-"))
	}
	return b.String()
}
func renderToolInstallRecord(header string, record session.ToolInstallRecord) string {
	record = session.NormalizeToolInstallRecord(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(string(record.Status), "-"))
	fmt.Fprintf(&b, "installer: %s\n", firstNonEmpty(record.Installer, "-"))
	fmt.Fprintf(&b, "install_ref: %s\n", firstNonEmpty(record.InstallRef, "-"))
	fmt.Fprintf(&b, "probe_status: %s\n", firstNonEmpty(string(record.ProbeStatus), "-"))
	fmt.Fprintf(&b, "probe_output: %s\n", firstNonEmpty(record.ProbeOutput, "-"))
	if !record.InstalledAt.IsZero() {
		fmt.Fprintf(&b, "installed_at: %s\n", record.InstalledAt.UTC().Format(time.RFC3339))
	}
	if !record.LastProbedAt.IsZero() {
		fmt.Fprintf(&b, "last_probed_at: %s\n", record.LastProbedAt.UTC().Format(time.RFC3339))
	}
	if !record.AttestedAt.IsZero() {
		fmt.Fprintf(&b, "attested_at: %s\n", record.AttestedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", record.UpdatedAt.UTC().Format(time.RFC3339))
	return strings.TrimRight(b.String(), "\n")
}

func renderToolInstallRecordList(records []session.ToolInstallRecord) string {
	var b strings.Builder
	b.WriteString("[TOOL_INSTALLS]")
	if len(records) == 0 {
		b.WriteString("\n- (none)")
		return b.String()
	}
	for _, record := range records {
		record = session.NormalizeToolInstallRecord(record)
		b.WriteString("\n- ")
		b.WriteString(record.ToolName)
		b.WriteString(" status=")
		b.WriteString(firstNonEmpty(string(record.Status), "-"))
		if strings.TrimSpace(record.InstallRef) != "" {
			b.WriteString(" install_ref=")
			b.WriteString(record.InstallRef)
		}
		if strings.TrimSpace(string(record.ProbeStatus)) != "" {
			b.WriteString(" probe_status=")
			b.WriteString(string(record.ProbeStatus))
		}
	}
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
