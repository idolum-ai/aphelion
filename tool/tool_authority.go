//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func (r *Registry) manifestCommandArtifactRefs(manifest ExternalToolManifest, command []string, label string) []session.RecordReference {
	manifest = NormalizeExternalToolManifest(manifest)
	if len(command) == 0 {
		return nil
	}
	first := strings.TrimSpace(command[0])
	if first == "" {
		return nil
	}
	if strings.HasPrefix(first, "./") || strings.HasPrefix(first, "../") || strings.HasPrefix(first, "/") {
		workdir, err := resolveWorkdir(r.workspace, manifest.Execution.Workdir)
		if err != nil {
			return nil
		}
		resolved := first
		if !strings.HasPrefix(first, "/") {
			resolved = filepath.Join(workdir, first)
		}
		return []session.RecordReference{{Kind: "file_path", Ref: resolved, Label: strings.TrimSpace(label)}}
	}
	return []session.RecordReference{{Kind: "command", Ref: first, Label: strings.TrimSpace(label)}}
}

func auditOutputArtifactRefs(output string) []session.RecordReference {
	trimmed := strings.TrimSpace(output)
	switch {
	case strings.HasPrefix(trimmed, "entry_path:"):
		ref := strings.TrimSpace(strings.TrimPrefix(trimmed, "entry_path:"))
		if ref == "" {
			return nil
		}
		return []session.RecordReference{{Kind: "file_path", Ref: ref, Label: "execution entry"}}
	case strings.HasPrefix(trimmed, "entry_command:"):
		ref := strings.TrimSpace(strings.TrimPrefix(trimmed, "entry_command:"))
		if ref == "" {
			return nil
		}
		return []session.RecordReference{{Kind: "command", Ref: ref, Label: "execution entry"}}
	case strings.HasPrefix(trimmed, "container_image:"):
		line := strings.SplitN(trimmed, "\n", 2)[0]
		ref := strings.TrimSpace(strings.TrimPrefix(line, "container_image:"))
		if ref == "" {
			return nil
		}
		return []session.RecordReference{{Kind: "container_image", Ref: ref, Label: "container image"}}
	default:
		return nil
	}
}

func runtimeAuthoredProbeRecord(record session.ToolProbeRecord) bool {
	return strings.HasPrefix(strings.TrimSpace(record.Rationale), "probe_run ")
}

func runtimeAuthoredAuditRecord(record session.ToolAuditRecord) bool {
	return strings.HasPrefix(strings.TrimSpace(record.Rationale), "audit_run ")
}

func externalToolInstallAnchors(record session.ToolInstallRecord) externalToolFingerprintSet {
	record = session.NormalizeToolInstallRecord(record)
	return externalToolFingerprintSet{
		Aggregate:            record.BaselineFingerprint,
		InstallRef:           record.BaselineInstallRef,
		ManifestHash:         record.BaselineManifestHash,
		WorkspaceFingerprint: record.BaselineWorkspaceFingerprint,
	}
}

func externalToolAuditAnchors(record session.ToolAuditRecord) externalToolFingerprintSet {
	record = session.NormalizeToolAuditRecord(record)
	return externalToolFingerprintSet{
		Aggregate:            record.BaselineFingerprint,
		InstallRef:           record.BaselineInstallRef,
		ManifestHash:         record.BaselineManifestHash,
		WorkspaceFingerprint: record.BaselineWorkspaceFingerprint,
	}
}

func externalToolProbeAnchors(record session.ToolProbeRecord) externalToolFingerprintSet {
	record = session.NormalizeToolProbeRecord(record)
	return externalToolFingerprintSet{
		Aggregate:            record.BaselineFingerprint,
		InstallRef:           record.BaselineInstallRef,
		ManifestHash:         record.BaselineManifestHash,
		WorkspaceFingerprint: record.BaselineWorkspaceFingerprint,
	}
}

func externalToolAnchorSetMatches(actual externalToolFingerprintSet, expected externalToolFingerprintSet) bool {
	return strings.TrimSpace(actual.Aggregate) != "" &&
		strings.TrimSpace(actual.Aggregate) == strings.TrimSpace(expected.Aggregate) &&
		strings.TrimSpace(actual.InstallRef) == strings.TrimSpace(expected.InstallRef) &&
		strings.TrimSpace(actual.ManifestHash) != "" &&
		strings.TrimSpace(actual.ManifestHash) == strings.TrimSpace(expected.ManifestHash) &&
		strings.TrimSpace(actual.WorkspaceFingerprint) == strings.TrimSpace(expected.WorkspaceFingerprint)
}

func setInstallRecordBaselineAnchors(record *session.ToolInstallRecord, fp externalToolFingerprintSet) {
	if record == nil {
		return
	}
	record.BaselineFingerprint = strings.TrimSpace(fp.Aggregate)
	record.CurrentFingerprint = strings.TrimSpace(fp.Aggregate)
	record.BaselineInstallRef = strings.TrimSpace(fp.InstallRef)
	record.CurrentInstallRef = strings.TrimSpace(fp.InstallRef)
	record.BaselineManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
	record.StaleReason = ""
	record.DriftSource = ""
}

func setInstallRecordCurrentAnchors(record *session.ToolInstallRecord, fp externalToolFingerprintSet) {
	if record == nil {
		return
	}
	record.CurrentFingerprint = strings.TrimSpace(fp.Aggregate)
	record.CurrentInstallRef = strings.TrimSpace(fp.InstallRef)
	record.CurrentManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
}

func setAuditRecordBaselineAnchors(record *session.ToolAuditRecord, fp externalToolFingerprintSet) {
	if record == nil {
		return
	}
	record.BaselineFingerprint = strings.TrimSpace(fp.Aggregate)
	record.CurrentFingerprint = strings.TrimSpace(fp.Aggregate)
	record.BaselineInstallRef = strings.TrimSpace(fp.InstallRef)
	record.CurrentInstallRef = strings.TrimSpace(fp.InstallRef)
	record.BaselineManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
	record.StaleReason = ""
	record.DriftSource = ""
}

func setAuditRecordCurrentAnchors(record *session.ToolAuditRecord, fp externalToolFingerprintSet) {
	if record == nil {
		return
	}
	record.CurrentFingerprint = strings.TrimSpace(fp.Aggregate)
	record.CurrentInstallRef = strings.TrimSpace(fp.InstallRef)
	record.CurrentManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
}

func setProbeRecordBaselineAnchors(record *session.ToolProbeRecord, fp externalToolFingerprintSet) {
	if record == nil {
		return
	}
	record.BaselineFingerprint = strings.TrimSpace(fp.Aggregate)
	record.CurrentFingerprint = strings.TrimSpace(fp.Aggregate)
	record.BaselineInstallRef = strings.TrimSpace(fp.InstallRef)
	record.CurrentInstallRef = strings.TrimSpace(fp.InstallRef)
	record.BaselineManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
	record.StaleReason = ""
	record.DriftSource = ""
}

func setProbeRecordCurrentAnchors(record *session.ToolProbeRecord, fp externalToolFingerprintSet) {
	if record == nil {
		return
	}
	record.CurrentFingerprint = strings.TrimSpace(fp.Aggregate)
	record.CurrentInstallRef = strings.TrimSpace(fp.InstallRef)
	record.CurrentManifestHash = strings.TrimSpace(fp.ManifestHash)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(fp.WorkspaceFingerprint)
}

func (r *Registry) ensureExternalToolFresh(manifest ExternalToolManifest, scope sandbox.Scope) error {
	record, exists, err := r.refreshExternalToolDrift(manifest, scope)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("external tool %q requires an install record", manifest.Name)
	}
	if record.Status == session.ToolInstallStatusStale {
		return fmt.Errorf("external tool %q is stale: %s", manifest.Name, firstNonEmpty(record.StaleReason, "verified baseline drift detected"))
	}
	if record.Status == session.ToolInstallStatusVerified && strings.TrimSpace(record.BaselineManifestHash) == "" {
		return fmt.Errorf("external tool %q is stale: missing verified baseline anchors", manifest.Name)
	}
	return nil
}

func (r *Registry) refreshExternalToolDrift(manifest ExternalToolManifest, scope sandbox.Scope) (session.ToolInstallRecord, bool, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	record, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil || !exists {
		return record, exists, err
	}
	if record.Status != session.ToolInstallStatusVerified {
		return record, true, nil
	}
	baseline := externalToolInstallAnchors(record)
	if strings.TrimSpace(baseline.Aggregate) == "" || strings.TrimSpace(baseline.ManifestHash) == "" {
		return r.markExternalToolStale(record, externalToolFingerprintSet{}, session.ToolDriftSourceMissingBaseline, "missing_baseline: verified install has no canonical baseline anchors")
	}
	current, err := externalToolFingerprints(manifest, scope.WorkingRoot, record.InstallRef)
	if err != nil {
		return r.markExternalToolStale(record, externalToolFingerprintSet{}, session.ToolDriftSourceFingerprintError, "fingerprint_error: "+err.Error())
	}
	switch {
	case strings.TrimSpace(baseline.InstallRef) != strings.TrimSpace(current.InstallRef):
		return r.markExternalToolStale(record, current, session.ToolDriftSourceInstallRefChanged, fmt.Sprintf("install_ref_changed: baseline=%s current=%s", baseline.InstallRef, current.InstallRef))
	case strings.TrimSpace(baseline.ManifestHash) != strings.TrimSpace(current.ManifestHash):
		return r.markExternalToolStale(record, current, session.ToolDriftSourceManifestDrift, fmt.Sprintf("manifest_drift: baseline=%s current=%s", baseline.ManifestHash, current.ManifestHash))
	case strings.TrimSpace(baseline.WorkspaceFingerprint) != strings.TrimSpace(current.WorkspaceFingerprint):
		if manifest.Execution.Mode == "container" {
			return r.markExternalToolStale(record, current, session.ToolDriftSourceContainerDrift, fmt.Sprintf("container_drift: baseline=%s current=%s", baseline.WorkspaceFingerprint, current.WorkspaceFingerprint))
		}
		return r.markExternalToolStale(record, current, session.ToolDriftSourceWorkspaceDrift, fmt.Sprintf("workspace_drift: baseline=%s current=%s", baseline.WorkspaceFingerprint, current.WorkspaceFingerprint))
	case strings.TrimSpace(baseline.Aggregate) != strings.TrimSpace(current.Aggregate):
		return r.markExternalToolStale(record, current, session.ToolDriftSourceFingerprintError, fmt.Sprintf("fingerprint_error: baseline=%s current=%s", baseline.Aggregate, current.Aggregate))
	}
	return record, true, nil
}

func (r *Registry) markExternalToolStale(record session.ToolInstallRecord, current externalToolFingerprintSet, source session.ToolDriftSource, reason string) (session.ToolInstallRecord, bool, error) {
	now := time.Now().UTC()
	reason = strings.TrimSpace(reason)
	record.Status = session.ToolInstallStatusStale
	setInstallRecordCurrentAnchors(&record, current)
	record.StaleReason = reason
	record.DriftSource = source
	record.AttestedAt = time.Time{}
	record.UpdatedAt = now
	stored, err := r.store.UpsertToolInstallRecord(record)
	if err != nil {
		return session.ToolInstallRecord{}, true, err
	}
	if audit, exists, err := r.store.ToolAuditRecord(record.ToolName); err == nil && exists {
		setAuditRecordCurrentAnchors(&audit, current)
		audit.StaleReason = reason
		audit.DriftSource = source
		audit.UpdatedAt = now
		_, _ = r.store.UpsertToolAuditRecord(audit)
	}
	if probe, exists, err := r.store.ToolProbeRecord(record.ToolName); err == nil && exists {
		setProbeRecordCurrentAnchors(&probe, current)
		probe.StaleReason = reason
		probe.DriftSource = source
		probe.UpdatedAt = now
		_, _ = r.store.UpsertToolProbeRecord(probe)
	}
	return stored, true, nil
}

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

func (r *Registry) toolAuthorityAuditRun(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority audit_run requires tool_name")
	}
	manifest, ok := r.externalManifestByName(toolName)
	if !ok {
		return "", fmt.Errorf("tool_authority audit_run requires an external tool manifest-backed tool_name")
	}
	installRecord, exists, err := r.store.ToolInstallRecord(manifest.Name)
	if err != nil {
		return "", err
	} else if !exists {
		return "", fmt.Errorf("external tool %q requires an install record before audit_run", manifest.Name)
	}
	prevAudit, prevAuditExists, err := r.store.ToolAuditRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	output, fingerprint, err := r.runExternalManifestAudit(ctx, manifest, scope, installRecord.InstallRef)
	now := time.Now().UTC()
	record := session.ToolAuditRecord{ToolName: manifest.Name, AuditOutput: output, UpdatedAt: now, AuditedAt: now, ArtifactRefs: auditOutputArtifactRefs(output)}
	setAuditRecordCurrentAnchors(&record, fingerprint)
	if prevAuditExists {
		record.CreatedAt = prevAudit.CreatedAt
	}
	if err != nil {
		record.Status = session.ToolAuditStatusFailed
		record.Rationale = "audit_run could not resolve the declared execution entry"
		record.DriftSource = session.ToolDriftSourceAuditFailure
		if isExternalPolicyViolation(err) {
			record.Rationale = "audit_run failed due to policy_violation"
			record.DriftSource = session.ToolDriftSourcePolicyViolation
		}
		record.ConsecutiveFailures = prevAudit.ConsecutiveFailures + 1
		record.LastFailureAt = now
		stored, saveErr := r.store.UpsertToolAuditRecord(record)
		if saveErr != nil {
			return "", saveErr
		}
		if installRecord.Status == session.ToolInstallStatusVerified {
			installRecord.Status = session.ToolInstallStatusStale
			installRecord.AttestedAt = time.Time{}
			installRecord.StaleReason = string(record.DriftSource) + ": " + err.Error()
			installRecord.DriftSource = record.DriftSource
			installRecord.UpdatedAt = now
			_, _ = r.store.UpsertToolInstallRecord(installRecord)
		}
		_ = r.appendToolAuthorityEvent(key, core.ExecutionEventToolAuditUpdated, string(stored.Status), map[string]any{"tool_name": stored.ToolName, "status": string(stored.Status), "actor_role": strings.TrimSpace(string(actor.Role)), "actor_user_id": actor.TelegramUserID})
		return "", err
	}
	record.Status = session.ToolAuditStatusPassed
	if manifest.Execution.Mode == "container" {
		record.Rationale = "audit_run resolved the declared container image and health check"
	} else {
		record.Rationale = "audit_run resolved the declared execution entry"
	}
	setAuditRecordBaselineAnchors(&record, fingerprint)
	record.ConsecutiveFailures = 0
	record.LastFailureAt = time.Time{}
	stored, err := r.store.UpsertToolAuditRecord(record)
	if err != nil {
		return "", err
	}
	if err := r.appendToolAuthorityEvent(key, core.ExecutionEventToolAuditUpdated, string(stored.Status), map[string]any{"tool_name": stored.ToolName, "status": string(stored.Status), "actor_role": strings.TrimSpace(string(actor.Role)), "actor_user_id": actor.TelegramUserID}); err != nil {
		return "", err
	}
	return renderToolAuditRecord("[TOOL_AUDIT]", stored), nil
}

func (r *Registry) toolAuthorityAuditShow(in toolAuthorityInput, scope sandbox.Scope) (string, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return "", fmt.Errorf("tool_authority audit_show requires tool_name")
	}
	if manifest, ok := r.externalManifestByName(toolName); ok {
		if _, _, err := r.refreshExternalToolDrift(manifest, scope); err != nil {
			return "", err
		}
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

func (r *Registry) runExternalManifestAudit(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, installRef string) (string, externalToolFingerprintSet, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if manifest.Execution.Mode == "container" {
		return r.runExternalContainerManifestAudit(ctx, manifest, scope, installRef)
	}
	if err := validateExternalProcessPolicy(manifest); err != nil {
		return "", externalToolFingerprintSet{}, err
	}
	workdir, err := resolveWorkdir(scope.WorkingRoot, manifest.Execution.Workdir)
	if err != nil {
		return "", externalToolFingerprintSet{}, err
	}
	entry := strings.TrimSpace(manifest.Execution.Entry)
	if entry == "" {
		return "", externalToolFingerprintSet{}, fmt.Errorf("external tool %q execution entry is empty", manifest.Name)
	}
	firstToken := strings.Fields(entry)
	if len(firstToken) == 0 {
		return "", externalToolFingerprintSet{}, fmt.Errorf("external tool %q execution entry is empty", manifest.Name)
	}
	target := firstToken[0]
	output := ""
	if strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") {
		resolved := target
		if !strings.HasPrefix(target, "/") {
			resolved = filepath.Join(workdir, target)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: entry path does not exist", manifest.Name)
			}
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit stat failed: %w", manifest.Name, err)
		}
		if info.IsDir() {
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: entry path is a directory", manifest.Name)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: entry path is not executable", manifest.Name)
		}
		if err := r.auditExternalLocalEntryLoadability(ctx, manifest, scope, resolved, workdir); err != nil {
			return fmt.Sprintf("entry_path: %s", resolved), externalToolFingerprintSet{}, err
		}
		output = fmt.Sprintf("entry_path: %s", resolved)
	} else {
		if _, err := exec.LookPath(target); err != nil {
			return fmt.Sprintf("entry_command: %s", target), externalToolFingerprintSet{}, fmt.Errorf("external tool %q import audit failed: command %q is not on PATH", manifest.Name, target)
		}
		output = fmt.Sprintf("entry_command: %s", target)
	}
	if len(manifest.Audit.Command) > 0 {
		auditOutput, err := r.runExternalManifestCommand(ctx, manifest, manifest.Audit.Command, scope)
		if err != nil {
			return output, externalToolFingerprintSet{}, err
		}
		if expected := strings.TrimSpace(manifest.Audit.ExpectedOutputContains); expected != "" && !strings.Contains(auditOutput, expected) {
			return output, externalToolFingerprintSet{}, fmt.Errorf("external tool %q audit output did not contain expected text %q", manifest.Name, expected)
		}
		if strings.TrimSpace(auditOutput) != "" {
			output = output + "\naudit_output: " + strings.TrimSpace(auditOutput)
		}
	}
	fingerprint, err := externalToolFingerprints(manifest, scope.WorkingRoot, installRef)
	if err != nil {
		return output, externalToolFingerprintSet{}, err
	}
	return output, fingerprint, nil
}

func (r *Registry) runExternalContainerManifestAudit(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, installRef string) (string, externalToolFingerprintSet, error) {
	image := strings.TrimSpace(firstNonEmpty(manifest.Container.Image, manifest.Execution.Entry))
	if image == "" {
		return "container_image: -", externalToolFingerprintSet{}, fmt.Errorf("external tool %q container audit failed: container image is required", manifest.Name)
	}
	if strings.TrimSpace(manifest.Container.Digest) == "" && strings.TrimSpace(manifest.Container.BuildRef) == "" {
		return "container_image: " + image, externalToolFingerprintSet{}, fmt.Errorf("external tool %q container audit failed: digest or build_ref is required", manifest.Name)
	}
	output := "container_image: " + image
	if strings.TrimSpace(manifest.Container.Digest) != "" {
		output += "\ncontainer_digest: " + strings.TrimSpace(manifest.Container.Digest)
	}
	if strings.TrimSpace(manifest.Container.BuildRef) != "" {
		output += "\ncontainer_build_ref: " + strings.TrimSpace(manifest.Container.BuildRef)
	}
	if len(manifest.Container.Healthcheck.Command) > 0 {
		healthOutput, err := r.runExternalManifestCommand(ctx, manifest, manifest.Container.Healthcheck.Command, scope)
		if err != nil {
			return output, externalToolFingerprintSet{}, fmt.Errorf("external tool %q container health check failed: %w", manifest.Name, err)
		}
		if expected := strings.TrimSpace(manifest.Container.Healthcheck.ExpectedOutputContains); expected != "" && !strings.Contains(healthOutput, expected) {
			return output, externalToolFingerprintSet{}, fmt.Errorf("external tool %q container health check output did not contain expected text %q", manifest.Name, expected)
		}
		if strings.TrimSpace(healthOutput) != "" {
			output += "\nhealthcheck_output: " + strings.TrimSpace(healthOutput)
		}
	}
	fingerprint, err := externalToolFingerprints(manifest, scope.WorkingRoot, installRef)
	if err != nil {
		return output, externalToolFingerprintSet{}, err
	}
	return output, fingerprint, nil
}

func (r *Registry) auditExternalLocalEntryLoadability(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, entryPath string, workdir string) error {
	interpreter, kind, err := discoverExternalEntryInterpreter(entryPath)
	if err != nil {
		return fmt.Errorf("external tool %q import audit failed: %w", manifest.Name, err)
	}
	switch kind {
	case "shell":
		return r.runExternalAuditCheck(ctx, manifest, scope, workdir, []string{firstNonEmpty(interpreter, "bash"), "-n", entryPath})
	case "python":
		return r.runExternalAuditCheck(ctx, manifest, scope, workdir, []string{firstNonEmpty(interpreter, "python3"), "-m", "py_compile", entryPath})
	default:
		return nil
	}
}

func (r *Registry) runExternalAuditCheck(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope, workdir string, command []string) error {
	timeout := 10 * time.Second
	if manifest.Constraints.MaxRuntimeSeconds > 0 && time.Duration(manifest.Constraints.MaxRuntimeSeconds)*time.Second < timeout {
		timeout = time.Duration(manifest.Constraints.MaxRuntimeSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, runErr := r.runCommand(runCtx, scope, shellQuoteCommand(command), workdir)
	if runErr != nil {
		return fmt.Errorf("external tool %q import audit loadability check failed: %s", manifest.Name, renderOutput(stdout, stderr, r.maxOutputBytes))
	}
	return nil
}

func discoverExternalEntryInterpreter(path string) (string, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	firstLine := ""
	if idx := strings.IndexByte(string(raw), '\n'); idx >= 0 {
		firstLine = string(raw[:idx])
	} else {
		firstLine = string(raw)
	}
	interpreter := ""
	if strings.HasPrefix(firstLine, "#!") {
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(firstLine, "#!")))
		if len(fields) > 0 {
			interpreter = resolveShebangInterpreter(fields)
		}
	}
	ext := strings.ToLower(filepath.Ext(path))
	name := strings.ToLower(filepath.Base(interpreter))
	if interpreter != "" {
		if strings.Contains(interpreter, "/") {
			if _, err := os.Stat(interpreter); err != nil {
				return "", "", fmt.Errorf("interpreter %q is not available: %w", interpreter, err)
			}
		} else if _, err := exec.LookPath(interpreter); err != nil {
			return "", "", fmt.Errorf("interpreter %q is not on PATH", interpreter)
		}
	}
	switch {
	case strings.Contains(name, "bash"), strings.Contains(name, "sh"), ext == ".sh":
		if interpreter == "" {
			interpreter = "bash"
		}
		if _, err := exec.LookPath(interpreter); err != nil && !strings.Contains(interpreter, "/") {
			return "", "", fmt.Errorf("interpreter %q is not on PATH", interpreter)
		}
		return interpreter, "shell", nil
	case strings.Contains(name, "python"), ext == ".py":
		if interpreter == "" {
			interpreter = "python3"
		}
		if _, err := exec.LookPath(interpreter); err != nil && !strings.Contains(interpreter, "/") {
			return "", "", fmt.Errorf("interpreter %q is not on PATH", interpreter)
		}
		return interpreter, "python", nil
	default:
		return interpreter, "", nil
	}
}

func resolveShebangInterpreter(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	first := strings.TrimSpace(fields[0])
	if strings.HasSuffix(first, "/env") || first == "env" {
		for _, field := range fields[1:] {
			field = strings.TrimSpace(field)
			if field == "" || strings.HasPrefix(field, "-") {
				continue
			}
			return field
		}
		return ""
	}
	return first
}

func (r *Registry) toolAuthorityProbeRun(ctx context.Context, in toolAuthorityInput, actor principal.Principal, key session.SessionKey, scope sandbox.Scope) (string, error) {
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
	prevProbe, _, err := r.store.ToolProbeRecord(manifest.Name)
	if err != nil {
		return "", err
	}
	fingerprint, fingerprintErr := externalToolFingerprints(manifest, scope.WorkingRoot, record.InstallRef)
	probeOutput := ""
	if fingerprintErr != nil {
		err = fingerprintErr
	} else {
		probeOutput, err = r.runExternalManifestProbe(ctx, manifest, scope)
	}
	now := time.Now().UTC()
	probeRefs := r.manifestCommandArtifactRefs(manifest, manifest.Probe.Command, "probe command")
	record.ProbeOutput = probeOutput
	record.LastProbedAt = now
	record.ArtifactRefs = probeRefs
	if err != nil {
		record.ProbeStatus = session.ToolProbeStatusFailed
		record.Rationale = "probe_run failed against the declared probe command"
		driftSource := session.ToolDriftSourceProbeFailure
		if fingerprintErr != nil {
			driftSource = session.ToolDriftSourceFingerprintError
		}
		if isExternalPolicyViolation(err) {
			driftSource = session.ToolDriftSourcePolicyViolation
			record.Rationale = "probe_run failed due to policy_violation"
		}
		consecutiveFailures := prevProbe.ConsecutiveFailures + 1
		probeRecord := session.ToolProbeRecord{ToolName: manifest.Name, Status: session.ToolProbeStatusFailed, ProbeOutput: probeOutput, Rationale: record.Rationale, ArtifactRefs: probeRefs, ProbedAt: now, ConsecutiveFailures: consecutiveFailures, LastFailureAt: now, DriftSource: driftSource, StaleReason: err.Error()}
		setProbeRecordCurrentAnchors(&probeRecord, fingerprint)
		if _, saveProbeErr := r.store.UpsertToolProbeRecord(probeRecord); saveProbeErr != nil {
			return "", saveProbeErr
		}
		if record.Status == session.ToolInstallStatusVerified {
			if consecutiveFailures >= 3 {
				record.Status = session.ToolInstallStatusFailed
			} else {
				record.Status = session.ToolInstallStatusStale
			}
			record.StaleReason = string(driftSource) + ": " + err.Error()
			record.DriftSource = driftSource
		} else if record.Status == session.ToolInstallStatusStale && consecutiveFailures >= 3 {
			record.Status = session.ToolInstallStatusFailed
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
	record.Rationale = "probe_run passed against the declared probe command"
	probeRecord := session.ToolProbeRecord{ToolName: manifest.Name, Status: session.ToolProbeStatusPassed, ProbeOutput: probeOutput, Rationale: "probe_run passed against the declared probe command", ArtifactRefs: probeRefs, ProbedAt: now, ConsecutiveFailures: 0}
	setProbeRecordBaselineAnchors(&probeRecord, fingerprint)
	if _, err := r.store.UpsertToolProbeRecord(probeRecord); err != nil {
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

func (r *Registry) runExternalManifestCommand(ctx context.Context, manifest ExternalToolManifest, command []string, scope sandbox.Scope) (string, error) {
	return r.runExternalManifestLifecycleCommand(ctx, manifest, command, scope, "install execution")
}

func (r *Registry) runExternalManifestLifecycleCommand(ctx context.Context, manifest ExternalToolManifest, command []string, scope sandbox.Scope, label string) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if len(command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a command", manifest.Name)
	}
	label = firstNonEmpty(strings.TrimSpace(label), "command execution")
	if err := validateExternalProcessPolicy(manifest); err != nil {
		return "", err
	}
	workdir, err := resolveWorkdir(scope.WorkingRoot, manifest.Execution.Workdir)
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
	stdout, stderr, runErr := r.runCommand(runCtx, scope, shellQuoteCommand(command), workdir)
	output := renderOutput(stdout, stderr, r.maxOutputBytes)
	if runErr != nil {
		return output, fmt.Errorf("external tool %q %s failed: %s", manifest.Name, label, output)
	}
	return output, nil
}

func shellQuoteCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func (r *Registry) runExternalManifestProbe(ctx context.Context, manifest ExternalToolManifest, scope sandbox.Scope) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if len(manifest.Probe.Command) == 0 {
		return "", fmt.Errorf("external tool %q does not declare a probe command", manifest.Name)
	}
	output, runErr := r.runExternalManifestCommand(ctx, manifest, manifest.Probe.Command, scope)
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
	grant, grantOK, err := r.store.ActiveCapabilityGrant(session.CapabilityKindTool, toolName, principalID, "invoke")
	if err != nil {
		return "", err
	}
	allowed := registeredOK && registered.Registered && grantOK
	var b strings.Builder
	b.WriteString("[TOOL_ACCESS]\n")
	fmt.Fprintf(&b, "tool_name: %s\n", toolName)
	fmt.Fprintf(&b, "principal: %s\n", principalID)
	fmt.Fprintf(&b, "registered: %t\n", registeredOK && registered.Registered)
	fmt.Fprintf(&b, "capability_grant_active: %t\n", grantOK)
	if grantOK {
		fmt.Fprintf(&b, "capability_grant_id: %s\n", grant.GrantID)
	}
	fmt.Fprintf(&b, "allowed: %t\n", allowed)
	return b.String(), nil
}

func (r *Registry) appendToolAuthorityEvent(key session.SessionKey, eventType string, status string, payload map[string]any) error {
	return r.appendToolLifecycleEvent(key, "tool_authority", eventType, status, payload)
}

func (r *Registry) appendToolLifecycleEvent(key session.SessionKey, stage string, eventType string, status string, payload map[string]any) error {
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
		Stage:       strings.TrimSpace(stage),
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
		"- register | registered_show | registered_list",
		"- install_set | install_show | install_list | install_execute | rollback | uninstall",
		"- audit_run | audit_show | audit_list | probe_run | probe_show | probe_list",
		"- access_check",
	}, "\n")
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

func renderRecordTraceability(b *strings.Builder, rationale string, refs []session.RecordReference) {
	rationale = strings.TrimSpace(rationale)
	if rationale != "" {
		fmt.Fprintf(b, "rationale: %s\n", rationale)
	}
	for _, ref := range session.NormalizeRecordReferences(refs) {
		fmt.Fprintf(b, "artifact_ref: %s %s", ref.Kind, ref.Ref)
		if strings.TrimSpace(ref.Label) != "" {
			fmt.Fprintf(b, " label=%s", ref.Label)
		}
		b.WriteString("\n")
	}
}

func renderToolProbeRecord(header string, record session.ToolProbeRecord) string {
	record = session.NormalizeToolProbeRecord(record)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "tool_name: %s\n", record.ToolName)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(string(record.Status), "-"))
	fmt.Fprintf(&b, "probe_output: %s\n", firstNonEmpty(record.ProbeOutput, "-"))
	fmt.Fprintf(&b, "consecutive_failures: %d\n", record.ConsecutiveFailures)
	if fp := strings.TrimSpace(record.BaselineFingerprint); fp != "" {
		fmt.Fprintf(&b, "baseline_fingerprint: %s\n", fp)
	}
	if fp := strings.TrimSpace(record.CurrentFingerprint); fp != "" {
		fmt.Fprintf(&b, "current_fingerprint: %s\n", fp)
	}
	if hash := strings.TrimSpace(record.BaselineManifestHash); hash != "" {
		fmt.Fprintf(&b, "baseline_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentManifestHash); hash != "" {
		fmt.Fprintf(&b, "current_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.BaselineWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "baseline_workspace_fingerprint: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "current_workspace_fingerprint: %s\n", hash)
	}
	if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
		fmt.Fprintf(&b, "drift_source: %s\n", source)
	}
	if reason := strings.TrimSpace(record.StaleReason); reason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", reason)
	}
	renderRecordTraceability(&b, record.Rationale, record.ArtifactRefs)
	if !record.ProbedAt.IsZero() {
		fmt.Fprintf(&b, "probed_at: %s\n", record.ProbedAt.UTC().Format(time.RFC3339))
	}
	if !record.LastFailureAt.IsZero() {
		fmt.Fprintf(&b, "last_failure_at: %s\n", record.LastFailureAt.UTC().Format(time.RFC3339))
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
		if why := strings.TrimSpace(record.Rationale); why != "" {
			b.WriteString(" why=")
			b.WriteString(why)
		}
		if refs := len(session.NormalizeRecordReferences(record.ArtifactRefs)); refs > 0 {
			b.WriteString(" refs=")
			b.WriteString(strconv.Itoa(refs))
		}
		if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
			b.WriteString(" drift_source=")
			b.WriteString(source)
		}
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
	fmt.Fprintf(&b, "consecutive_failures: %d\n", record.ConsecutiveFailures)
	if fp := strings.TrimSpace(record.BaselineFingerprint); fp != "" {
		fmt.Fprintf(&b, "baseline_fingerprint: %s\n", fp)
	}
	if fp := strings.TrimSpace(record.CurrentFingerprint); fp != "" {
		fmt.Fprintf(&b, "current_fingerprint: %s\n", fp)
	}
	if hash := strings.TrimSpace(record.BaselineManifestHash); hash != "" {
		fmt.Fprintf(&b, "baseline_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentManifestHash); hash != "" {
		fmt.Fprintf(&b, "current_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.BaselineWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "baseline_workspace_fingerprint: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "current_workspace_fingerprint: %s\n", hash)
	}
	if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
		fmt.Fprintf(&b, "drift_source: %s\n", source)
	}
	if reason := strings.TrimSpace(record.StaleReason); reason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", reason)
	}
	renderRecordTraceability(&b, record.Rationale, record.ArtifactRefs)
	if !record.AuditedAt.IsZero() {
		fmt.Fprintf(&b, "audited_at: %s\n", record.AuditedAt.UTC().Format(time.RFC3339))
	}
	if !record.LastFailureAt.IsZero() {
		fmt.Fprintf(&b, "last_failure_at: %s\n", record.LastFailureAt.UTC().Format(time.RFC3339))
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
		if why := strings.TrimSpace(record.Rationale); why != "" {
			b.WriteString(" why=")
			b.WriteString(why)
		}
		if reason := strings.TrimSpace(record.StaleReason); reason != "" {
			b.WriteString(" stale_reason=")
			b.WriteString(reason)
		}
		if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
			b.WriteString(" drift_source=")
			b.WriteString(source)
		}
		if refs := len(session.NormalizeRecordReferences(record.ArtifactRefs)); refs > 0 {
			b.WriteString(" refs=")
			b.WriteString(strconv.Itoa(refs))
		}
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
	fmt.Fprintf(&b, "consecutive_failures: %d\n", record.ConsecutiveFailures)
	if fp := strings.TrimSpace(record.BaselineFingerprint); fp != "" {
		fmt.Fprintf(&b, "baseline_fingerprint: %s\n", fp)
	}
	if fp := strings.TrimSpace(record.CurrentFingerprint); fp != "" {
		fmt.Fprintf(&b, "current_fingerprint: %s\n", fp)
	}
	if hash := strings.TrimSpace(record.BaselineManifestHash); hash != "" {
		fmt.Fprintf(&b, "baseline_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentManifestHash); hash != "" {
		fmt.Fprintf(&b, "current_manifest_hash: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.BaselineWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "baseline_workspace_fingerprint: %s\n", hash)
	}
	if hash := strings.TrimSpace(record.CurrentWorkspaceFingerprint); hash != "" {
		fmt.Fprintf(&b, "current_workspace_fingerprint: %s\n", hash)
	}
	if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
		fmt.Fprintf(&b, "drift_source: %s\n", source)
	}
	if reason := strings.TrimSpace(record.StaleReason); reason != "" {
		fmt.Fprintf(&b, "stale_reason: %s\n", reason)
	}
	renderRecordTraceability(&b, record.Rationale, record.ArtifactRefs)
	if !record.InstalledAt.IsZero() {
		fmt.Fprintf(&b, "installed_at: %s\n", record.InstalledAt.UTC().Format(time.RFC3339))
	}
	if !record.LastProbedAt.IsZero() {
		fmt.Fprintf(&b, "last_probed_at: %s\n", record.LastProbedAt.UTC().Format(time.RFC3339))
	}
	if !record.AttestedAt.IsZero() {
		fmt.Fprintf(&b, "attested_at: %s\n", record.AttestedAt.UTC().Format(time.RFC3339))
	}
	if !record.LastFailureAt.IsZero() {
		fmt.Fprintf(&b, "last_failure_at: %s\n", record.LastFailureAt.UTC().Format(time.RFC3339))
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
		if reason := strings.TrimSpace(record.StaleReason); reason != "" {
			b.WriteString(" stale_reason=")
			b.WriteString(reason)
		}
		if source := strings.TrimSpace(string(record.DriftSource)); source != "" {
			b.WriteString(" drift_source=")
			b.WriteString(source)
		}
		if why := strings.TrimSpace(record.Rationale); why != "" {
			b.WriteString(" why=")
			b.WriteString(why)
		}
		if refs := len(session.NormalizeRecordReferences(record.ArtifactRefs)); refs > 0 {
			b.WriteString(" refs=")
			b.WriteString(strconv.Itoa(refs))
		}
	}
	return b.String()
}
