//go:build linux

package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) toolAuthorityInstallExecute(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
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
	installOutput, err := r.runExternalManifestCommand(ctx, manifest, manifest.Install.Command, scope)
	now := time.Now().UTC()
	commandRefs := r.manifestCommandArtifactRefs(manifest, manifest.Install.Command, "install command")
	record.ProbeOutput = strings.TrimSpace(installOutput)
	record.UpdatedAt = now
	record.ArtifactRefs = commandRefs
	if err != nil {
		record.Status = session.ToolInstallStatusFailed
		record.Rationale = "install_execute failed while running the manifest install command"
		if isExternalPolicyViolation(err) {
			record.Rationale = "install_execute failed due to policy_violation"
			record.DriftSource = session.ToolDriftSourcePolicyViolation
			record.StaleReason = err.Error()
		}
		record.ConsecutiveFailures++
		record.LastFailureAt = now
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
	record.Rationale = "install_execute ran the manifest install command"
	record.ConsecutiveFailures = 0
	record.LastFailureAt = time.Time{}
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

func (r *Registry) toolAuthorityRollback(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
	return r.toolAuthorityRetireExternal(ctx, in, actor, key, scope, "rollback")
}

func (r *Registry) toolAuthorityUninstall(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
	return r.toolAuthorityRetireExternal(ctx, in, actor, key, scope, "uninstall")
}

func (r *Registry) toolAuthorityRetireExternal(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope, mode string) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority %s requires tool_name", mode)
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority %s requires an external tool manifest-backed tool_name", mode)
	}
	manifest = NormalizeExternalToolManifest(manifest)
	record, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("external tool %q requires an install record before %s", manifest.Name, mode)
	}

	var command []string
	eventType := core.ExecutionEventToolRollbackApplied
	header := "[TOOL_ROLLBACK]"
	driftSource := session.ToolDriftSourceRollback
	rationale := firstNonEmpty(strings.TrimSpace(in.Rationale), "rollback withdrew the external tool registration, active grants, and verified install evidence")
	commandLabel := "rollback command"
	switch mode {
	case "uninstall":
		command = manifest.Uninstall.Command
		eventType = core.ExecutionEventToolRemovalApplied
		header = "[TOOL_UNINSTALL]"
		driftSource = session.ToolDriftSourceRemoval
		rationale = firstNonEmpty(strings.TrimSpace(in.Rationale), "uninstall retired the external tool registration, active grants, and verified install evidence")
		commandLabel = "uninstall command"
	default:
		command = manifest.Rollback.Command
	}

	now := time.Now().UTC()
	commandRefs := r.manifestCommandArtifactRefs(manifest, command, commandLabel)
	commandOutput := ""
	if len(command) > 0 {
		commandOutput, err = r.runExternalManifestLifecycleCommand(ctx, manifest, command, scope, strings.TrimSuffix(commandLabel, " command")+" execution")
		record.ProbeOutput = strings.TrimSpace(commandOutput)
		record.ArtifactRefs = commandRefs
		record.UpdatedAt = now
		if err != nil {
			record.Status = session.ToolInstallStatusFailed
			record.Rationale = fmt.Sprintf("%s failed while running the manifest %s", mode, commandLabel)
			record.DriftSource = driftSource
			if isExternalPolicyViolation(err) {
				record.Rationale = fmt.Sprintf("%s failed due to policy_violation", mode)
				record.DriftSource = session.ToolDriftSourcePolicyViolation
			}
			record.StaleReason = err.Error()
			record.ConsecutiveFailures++
			record.LastFailureAt = now
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
			_ = r.appendToolAuthorityEvent(key, eventType, "failed", map[string]any{
				"tool_name":     stored.ToolName,
				"status":        string(stored.Status),
				"reason":        record.StaleReason,
				"actor_role":    strings.TrimSpace(string(actor.Role)),
				"actor_user_id": actor.TelegramUserID,
			})
			return "", err
		}
	}

	record.Status = session.ToolInstallStatusStale
	record.Rationale = rationale
	record.StaleReason = string(driftSource) + ": " + rationale
	record.DriftSource = driftSource
	record.AttestedAt = time.Time{}
	record.UpdatedAt = now
	if len(commandRefs) > 0 {
		record.ArtifactRefs = commandRefs
	}
	stored, err := r.store.UpsertToolInstallRecord(record)
	if err != nil {
		return "", err
	}
	if audit, exists, err := r.store.ToolAuditRecord(manifest.Name); err == nil && exists {
		audit.StaleReason = record.StaleReason
		audit.DriftSource = driftSource
		audit.UpdatedAt = now
		_, _ = r.store.UpsertToolAuditRecord(audit)
	}
	if probe, exists, err := r.store.ToolProbeRecord(manifest.Name); err == nil && exists {
		probe.StaleReason = record.StaleReason
		probe.DriftSource = driftSource
		probe.UpdatedAt = now
		_, _ = r.store.UpsertToolProbeRecord(probe)
	}

	registrationDisabled, err := r.disableRegisteredTool(manifest.Name, actor, key)
	if err != nil {
		return "", err
	}
	revokedGrantIDs, err := r.revokeToolCapabilityGrants(manifest.Name, rationale, actor, key, now)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, core.ExecutionEventToolInstallUpdated, string(stored.Status), map[string]any{
		"tool_name":     stored.ToolName,
		"status":        string(stored.Status),
		"install_ref":   stored.InstallRef,
		"drift_source":  string(stored.DriftSource),
		"actor_role":    strings.TrimSpace(string(actor.Role)),
		"actor_user_id": actor.TelegramUserID,
	}); err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, eventType, string(stored.Status), map[string]any{
		"tool_name":              stored.ToolName,
		"status":                 string(stored.Status),
		"drift_source":           string(stored.DriftSource),
		"rationale":              rationale,
		"registration_disabled":  registrationDisabled,
		"revoked_capability_ids": revokedGrantIDs,
		"actor_role":             strings.TrimSpace(string(actor.Role)),
		"actor_user_id":          actor.TelegramUserID,
	}); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(renderToolInstallRecord(header, stored))
	fmt.Fprintf(&b, "\nregistration_disabled: %t\n", registrationDisabled)
	fmt.Fprintf(&b, "revoked_capability_grants: %d\n", len(revokedGrantIDs))
	for _, grantID := range revokedGrantIDs {
		fmt.Fprintf(&b, "revoked_capability_grant_id: %s\n", grantID)
	}
	if strings.TrimSpace(commandOutput) != "" {
		fmt.Fprintf(&b, "command_output: %s\n", strings.TrimSpace(commandOutput))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (r *Registry) disableRegisteredTool(toolName string, actor principal.Principal, key session.SessionKey) (bool, error) {
	registered, ok, err := r.store.RegisteredTool(toolName)
	if err != nil || !ok || !registered.Registered {
		return false, err
	}
	registered.Registered = false
	stored, err := r.store.UpsertRegisteredTool(registered)
	if err != nil {
		return false, err
	}
	if err := r.appendToolAuthorityEvent(
		key,
		core.ExecutionEventToolRegistered,
		boolToStatus(stored.Registered),
		map[string]any{
			"tool_name":          stored.ToolName,
			"registered":         stored.Registered,
			"implementation_ref": stored.ImplementationRef,
			"actor_role":         strings.TrimSpace(string(actor.Role)),
			"actor_user_id":      actor.TelegramUserID,
		},
	); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Registry) revokeToolCapabilityGrants(toolName string, rationale string, actor principal.Principal, key session.SessionKey, now time.Time) ([]string, error) {
	grants, err := r.store.CapabilityGrants(500, session.CapabilityGrantStatusActive, session.CapabilityKindTool, "")
	if err != nil {
		return nil, err
	}
	revoked := make([]string, 0)
	for _, grant := range grants {
		if strings.TrimSpace(grant.TargetResource) != toolName {
			continue
		}
		grant.Status = session.CapabilityGrantStatusRevoked
		grant.StaleReason = strings.TrimSpace(rationale)
		grant.RevokedAt = now
		grant.UpdatedAt = now
		stored, err := r.store.UpsertCapabilityGrant(grant)
		if err != nil {
			return nil, err
		}
		revoked = append(revoked, stored.GrantID)
		if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityGrantChanged, string(stored.Status), map[string]any{
			"grant_id":        stored.GrantID,
			"request_id":      stored.RequestID,
			"kind":            string(stored.Kind),
			"target_resource": stored.TargetResource,
			"granted_to":      stored.GrantedTo,
			"status":          string(stored.Status),
			"revoked_by":      toolAuthorityPrincipalDisplay(actor),
			"rationale":       strings.TrimSpace(rationale),
		}); err != nil {
			return nil, err
		}
	}
	return revoked, nil
}
