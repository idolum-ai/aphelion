//go:build linux

package tool

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

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
