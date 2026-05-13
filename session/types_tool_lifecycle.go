//go:build linux

package session

import (
	"strings"
	"time"
)

type RegisteredTool struct {
	ToolName          string    `json:"tool_name"`
	ImplementationRef string    `json:"implementation_ref,omitempty"`
	Registered        bool      `json:"registered"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type ToolInstallStatus string

type ToolProbeStatus string

type ToolAuditStatus string

type ToolDriftSource string

type ToolInstallRecord struct {
	ToolName                     string            `json:"tool_name"`
	Installer                    string            `json:"installer,omitempty"`
	InstallRef                   string            `json:"install_ref,omitempty"`
	Status                       ToolInstallStatus `json:"status,omitempty"`
	ProbeStatus                  ToolProbeStatus   `json:"probe_status,omitempty"`
	ProbeOutput                  string            `json:"probe_output,omitempty"`
	Rationale                    string            `json:"rationale,omitempty"`
	ArtifactRefs                 []RecordReference `json:"artifact_refs,omitempty"`
	BaselineFingerprint          string            `json:"baseline_fingerprint,omitempty"`
	CurrentFingerprint           string            `json:"current_fingerprint,omitempty"`
	BaselineInstallRef           string            `json:"baseline_install_ref,omitempty"`
	CurrentInstallRef            string            `json:"current_install_ref,omitempty"`
	BaselineManifestHash         string            `json:"baseline_manifest_hash,omitempty"`
	CurrentManifestHash          string            `json:"current_manifest_hash,omitempty"`
	BaselineWorkspaceFingerprint string            `json:"baseline_workspace_fingerprint,omitempty"`
	CurrentWorkspaceFingerprint  string            `json:"current_workspace_fingerprint,omitempty"`
	StaleReason                  string            `json:"stale_reason,omitempty"`
	DriftSource                  ToolDriftSource   `json:"drift_source,omitempty"`
	ConsecutiveFailures          int               `json:"consecutive_failures,omitempty"`
	CreatedAt                    time.Time         `json:"created_at,omitempty"`
	UpdatedAt                    time.Time         `json:"updated_at,omitempty"`
	InstalledAt                  time.Time         `json:"installed_at,omitempty"`
	LastProbedAt                 time.Time         `json:"last_probed_at,omitempty"`
	LastFailureAt                time.Time         `json:"last_failure_at,omitempty"`
	AttestedAt                   time.Time         `json:"attested_at,omitempty"`
}

type ToolAuditRecord struct {
	ToolName                     string            `json:"tool_name"`
	Status                       ToolAuditStatus   `json:"status,omitempty"`
	AuditOutput                  string            `json:"audit_output,omitempty"`
	Rationale                    string            `json:"rationale,omitempty"`
	ArtifactRefs                 []RecordReference `json:"artifact_refs,omitempty"`
	BaselineFingerprint          string            `json:"baseline_fingerprint,omitempty"`
	CurrentFingerprint           string            `json:"current_fingerprint,omitempty"`
	BaselineInstallRef           string            `json:"baseline_install_ref,omitempty"`
	CurrentInstallRef            string            `json:"current_install_ref,omitempty"`
	BaselineManifestHash         string            `json:"baseline_manifest_hash,omitempty"`
	CurrentManifestHash          string            `json:"current_manifest_hash,omitempty"`
	BaselineWorkspaceFingerprint string            `json:"baseline_workspace_fingerprint,omitempty"`
	CurrentWorkspaceFingerprint  string            `json:"current_workspace_fingerprint,omitempty"`
	StaleReason                  string            `json:"stale_reason,omitempty"`
	DriftSource                  ToolDriftSource   `json:"drift_source,omitempty"`
	ConsecutiveFailures          int               `json:"consecutive_failures,omitempty"`
	CreatedAt                    time.Time         `json:"created_at,omitempty"`
	UpdatedAt                    time.Time         `json:"updated_at,omitempty"`
	AuditedAt                    time.Time         `json:"audited_at,omitempty"`
	LastFailureAt                time.Time         `json:"last_failure_at,omitempty"`
}

type ToolProbeRecord struct {
	ToolName                     string            `json:"tool_name"`
	Status                       ToolProbeStatus   `json:"status,omitempty"`
	ProbeOutput                  string            `json:"probe_output,omitempty"`
	Rationale                    string            `json:"rationale,omitempty"`
	ArtifactRefs                 []RecordReference `json:"artifact_refs,omitempty"`
	BaselineFingerprint          string            `json:"baseline_fingerprint,omitempty"`
	CurrentFingerprint           string            `json:"current_fingerprint,omitempty"`
	BaselineInstallRef           string            `json:"baseline_install_ref,omitempty"`
	CurrentInstallRef            string            `json:"current_install_ref,omitempty"`
	BaselineManifestHash         string            `json:"baseline_manifest_hash,omitempty"`
	CurrentManifestHash          string            `json:"current_manifest_hash,omitempty"`
	BaselineWorkspaceFingerprint string            `json:"baseline_workspace_fingerprint,omitempty"`
	CurrentWorkspaceFingerprint  string            `json:"current_workspace_fingerprint,omitempty"`
	StaleReason                  string            `json:"stale_reason,omitempty"`
	DriftSource                  ToolDriftSource   `json:"drift_source,omitempty"`
	ConsecutiveFailures          int               `json:"consecutive_failures,omitempty"`
	CreatedAt                    time.Time         `json:"created_at,omitempty"`
	UpdatedAt                    time.Time         `json:"updated_at,omitempty"`
	ProbedAt                     time.Time         `json:"probed_at,omitempty"`
	LastFailureAt                time.Time         `json:"last_failure_at,omitempty"`
}

func NormalizeRegisteredTool(tool RegisteredTool) RegisteredTool {
	tool.ToolName = strings.TrimSpace(tool.ToolName)
	tool.ImplementationRef = strings.TrimSpace(tool.ImplementationRef)
	if tool.CreatedAt.IsZero() && tool.ToolName != "" {
		tool.CreatedAt = time.Now().UTC()
	}
	if tool.UpdatedAt.IsZero() && tool.ToolName != "" {
		tool.UpdatedAt = time.Now().UTC()
	}
	return tool
}

func NormalizeToolInstallStatus(status ToolInstallStatus) ToolInstallStatus {
	switch ToolInstallStatus(strings.TrimSpace(string(status))) {
	case ToolInstallStatusPending:
		return ToolInstallStatusPending
	case ToolInstallStatusInstalled:
		return ToolInstallStatusInstalled
	case ToolInstallStatusVerified:
		return ToolInstallStatusVerified
	case ToolInstallStatusFailed:
		return ToolInstallStatusFailed
	case ToolInstallStatusStale:
		return ToolInstallStatusStale
	default:
		return ""
	}
}

func NormalizeToolProbeStatus(status ToolProbeStatus) ToolProbeStatus {
	switch ToolProbeStatus(strings.TrimSpace(string(status))) {
	case ToolProbeStatusPassed:
		return ToolProbeStatusPassed
	case ToolProbeStatusFailed:
		return ToolProbeStatusFailed
	default:
		return ""
	}
}

func NormalizeToolAuditStatus(status ToolAuditStatus) ToolAuditStatus {
	switch ToolAuditStatus(strings.TrimSpace(string(status))) {
	case ToolAuditStatusPassed:
		return ToolAuditStatusPassed
	case ToolAuditStatusFailed:
		return ToolAuditStatusFailed
	default:
		return ""
	}
}

func NormalizeToolInstallRecord(record ToolInstallRecord) ToolInstallRecord {
	record.ToolName = strings.TrimSpace(record.ToolName)
	record.Installer = strings.TrimSpace(record.Installer)
	record.InstallRef = strings.TrimSpace(record.InstallRef)
	record.ProbeOutput = strings.TrimSpace(record.ProbeOutput)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.BaselineFingerprint = strings.TrimSpace(record.BaselineFingerprint)
	record.CurrentFingerprint = strings.TrimSpace(record.CurrentFingerprint)
	record.BaselineInstallRef = strings.TrimSpace(record.BaselineInstallRef)
	record.CurrentInstallRef = strings.TrimSpace(record.CurrentInstallRef)
	record.BaselineManifestHash = strings.TrimSpace(record.BaselineManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(record.CurrentManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(record.BaselineWorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(record.CurrentWorkspaceFingerprint)
	record.StaleReason = strings.TrimSpace(record.StaleReason)
	record.DriftSource = ToolDriftSource(strings.TrimSpace(string(record.DriftSource)))
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	record.Status = NormalizeToolInstallStatus(record.Status)
	record.ProbeStatus = NormalizeToolProbeStatus(record.ProbeStatus)
	if record.CreatedAt.IsZero() && record.ToolName != "" {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.ToolName != "" {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func NormalizeToolAuditRecord(record ToolAuditRecord) ToolAuditRecord {
	record.ToolName = strings.TrimSpace(record.ToolName)
	record.Status = NormalizeToolAuditStatus(record.Status)
	record.AuditOutput = strings.TrimSpace(record.AuditOutput)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.BaselineFingerprint = strings.TrimSpace(record.BaselineFingerprint)
	record.CurrentFingerprint = strings.TrimSpace(record.CurrentFingerprint)
	record.BaselineInstallRef = strings.TrimSpace(record.BaselineInstallRef)
	record.CurrentInstallRef = strings.TrimSpace(record.CurrentInstallRef)
	record.BaselineManifestHash = strings.TrimSpace(record.BaselineManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(record.CurrentManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(record.BaselineWorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(record.CurrentWorkspaceFingerprint)
	record.StaleReason = strings.TrimSpace(record.StaleReason)
	record.DriftSource = ToolDriftSource(strings.TrimSpace(string(record.DriftSource)))
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	if record.ConsecutiveFailures < 0 {
		record.ConsecutiveFailures = 0
	}
	if record.CreatedAt.IsZero() && record.ToolName != "" {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.ToolName != "" {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func NormalizeToolProbeRecord(record ToolProbeRecord) ToolProbeRecord {
	record.ToolName = strings.TrimSpace(record.ToolName)
	record.Status = NormalizeToolProbeStatus(record.Status)
	record.ProbeOutput = strings.TrimSpace(record.ProbeOutput)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.BaselineFingerprint = strings.TrimSpace(record.BaselineFingerprint)
	record.CurrentFingerprint = strings.TrimSpace(record.CurrentFingerprint)
	record.BaselineInstallRef = strings.TrimSpace(record.BaselineInstallRef)
	record.CurrentInstallRef = strings.TrimSpace(record.CurrentInstallRef)
	record.BaselineManifestHash = strings.TrimSpace(record.BaselineManifestHash)
	record.CurrentManifestHash = strings.TrimSpace(record.CurrentManifestHash)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(record.BaselineWorkspaceFingerprint)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(record.CurrentWorkspaceFingerprint)
	record.StaleReason = strings.TrimSpace(record.StaleReason)
	record.DriftSource = ToolDriftSource(strings.TrimSpace(string(record.DriftSource)))
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	if record.ConsecutiveFailures < 0 {
		record.ConsecutiveFailures = 0
	}
	if record.CreatedAt.IsZero() && record.ToolName != "" {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.ToolName != "" {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}
