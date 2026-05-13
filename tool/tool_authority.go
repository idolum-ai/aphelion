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
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) toolAuthority(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
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
	case "register":
		return r.toolAuthorityRegister(in, p, key, scope)
	case "registered_show":
		return r.toolAuthorityRegisteredShow(in)
	case "registered_list":
		return r.toolAuthorityRegisteredList(in)
	case "install_set":
		return r.toolAuthorityInstallSet(in, p, key, scope)
	case "install_show":
		return r.toolAuthorityInstallShow(in, scope)
	case "install_list":
		return r.toolAuthorityInstallList(in)
	case "install_execute":
		return r.toolAuthorityInstallExecute(ctx, in, p, key, scope)
	case "rollback":
		return r.toolAuthorityRollback(ctx, in, p, key, scope)
	case "uninstall":
		return r.toolAuthorityUninstall(ctx, in, p, key, scope)
	case "audit_run":
		return r.toolAuthorityAuditRun(ctx, in, p, key, scope)
	case "audit_show":
		return r.toolAuthorityAuditShow(in, scope)
	case "audit_list":
		return r.toolAuthorityAuditList(in)
	case "probe_run":
		return r.toolAuthorityProbeRun(ctx, in, p, key, scope)
	case "probe_show":
		return r.toolAuthorityProbeShow(in)
	case "probe_list":
		return r.toolAuthorityProbeList(in)
	case "access_check":
		return r.toolAuthorityAccessCheck(in)
	default:
		return "", fmt.Errorf("tool_authority action %q is not supported", action)
	}
}

func (r *Registry) toolAuthorityRegister(in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
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
		if err := r.ensureExternalToolFresh(manifest, scope); err != nil {
			return "", err
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

	if err := r.appendToolAuthorityEvent(
		key,
		core.ExecutionEventToolRegistered,
		boolToStatus(record.Registered),
		map[string]any{
			"tool_name":           record.ToolName,
			"registered":          record.Registered,
			"implementation_ref":  record.ImplementationRef,
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

func (r *Registry) toolAuthorityInstallSet(in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
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
	if strings.TrimSpace(in.ProbeStatus) != "" || strings.TrimSpace(in.ProbeOutput) != "" {
		return "", fmt.Errorf("tool_authority install_set no longer accepts probe_status or probe_output; use probe_run to author runtime probe evidence")
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
	record.CurrentInstallRef = strings.TrimSpace(record.InstallRef)
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
		if !ok || probe.Status != session.ToolProbeStatusPassed || probe.ProbedAt.IsZero() || (!record.InstalledAt.IsZero() && probe.ProbedAt.Before(record.InstalledAt)) || !runtimeAuthoredProbeRecord(probe) {
			return "", fmt.Errorf("tool_authority install_set verified status requires a passed runtime-authored probe_run record")
		}
		audit, ok, err := r.store.ToolAuditRecord(manifest.Name)
		if err != nil {
			return "", err
		}
		if !ok || audit.Status != session.ToolAuditStatusPassed || audit.AuditedAt.IsZero() || (!record.InstalledAt.IsZero() && audit.AuditedAt.Before(record.InstalledAt)) || !runtimeAuthoredAuditRecord(audit) {
			return "", fmt.Errorf("tool_authority install_set verified status requires a passed runtime-authored audit_run record")
		}
		fingerprint, err := externalToolFingerprints(manifest, scope.WorkingRoot, record.InstallRef)
		if err != nil {
			return "", err
		}
		if !externalToolAnchorSetMatches(externalToolAuditAnchors(audit), fingerprint) {
			return "", fmt.Errorf("tool_authority install_set verified status requires audit_run to be fresh against the current install_ref, manifest hash, and workspace fingerprint")
		}
		if !externalToolAnchorSetMatches(externalToolProbeAnchors(probe), fingerprint) {
			return "", fmt.Errorf("tool_authority install_set verified status requires probe_run to be fresh against the current install_ref, manifest hash, and workspace fingerprint")
		}
		record.ProbeStatus = probe.Status
		record.ProbeOutput = probe.ProbeOutput
		record.LastProbedAt = probe.ProbedAt
		setInstallRecordBaselineAnchors(&record, fingerprint)
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

func (r *Registry) toolAuthorityInstallShow(in toolAuthorityInput, scope sandbox.Scope) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority install_show requires tool_name")
	}
	if manifest, ok := r.externalManifestByName(toolName); ok {
		if _, _, err := r.refreshExternalToolDrift(manifest, scope); err != nil {
			return "", err
		}
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
