//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertToolAuditRecord(record ToolAuditRecord) (ToolAuditRecord, error) {
	record = NormalizeToolAuditRecord(record)
	if record.ToolName == "" {
		return ToolAuditRecord{}, fmt.Errorf("tool audit record tool_name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
			INSERT INTO tool_audit_records(tool_name, status, audit_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, audited_at, last_failure_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(tool_name) DO UPDATE SET
				status = excluded.status,
				audit_output = excluded.audit_output,
				rationale = excluded.rationale,
				artifact_refs_json = excluded.artifact_refs_json,
				baseline_fingerprint = excluded.baseline_fingerprint,
				current_fingerprint = excluded.current_fingerprint,
				baseline_install_ref = excluded.baseline_install_ref,
				current_install_ref = excluded.current_install_ref,
				baseline_manifest_hash = excluded.baseline_manifest_hash,
				current_manifest_hash = excluded.current_manifest_hash,
				baseline_workspace_fingerprint = excluded.baseline_workspace_fingerprint,
				current_workspace_fingerprint = excluded.current_workspace_fingerprint,
				stale_reason = excluded.stale_reason,
				drift_source = excluded.drift_source,
				consecutive_failures = excluded.consecutive_failures,
				updated_at = excluded.updated_at,
				audited_at = excluded.audited_at,
				last_failure_at = excluded.last_failure_at
		`, record.ToolName, string(record.Status), record.AuditOutput, record.Rationale, encodeRecordReferences(record.ArtifactRefs), record.BaselineFingerprint, record.CurrentFingerprint, record.BaselineInstallRef, record.CurrentInstallRef, record.BaselineManifestHash, record.CurrentManifestHash, record.BaselineWorkspaceFingerprint, record.CurrentWorkspaceFingerprint, record.StaleReason, string(record.DriftSource), record.ConsecutiveFailures, createdAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano), nullableTimeRFC3339(record.AuditedAt), nullableTimeRFC3339(record.LastFailureAt)); err != nil {
		return ToolAuditRecord{}, fmt.Errorf("upsert tool audit record: %w", err)
	}
	stored, ok, err := s.ToolAuditRecord(record.ToolName)
	if err != nil {
		return ToolAuditRecord{}, err
	}
	if !ok {
		return ToolAuditRecord{}, fmt.Errorf("tool audit record %q not found after upsert", record.ToolName)
	}
	return stored, nil
}

func (s *SQLiteStore) ToolAuditRecord(toolName string) (ToolAuditRecord, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolAuditRecord{}, false, nil
	}
	var (
		record                 ToolAuditRecord
		statusRaw              string
		artifactRefsRaw        string
		baselineFingerprintRaw string
		currentFingerprintRaw  string
		baselineInstallRefRaw  string
		currentInstallRefRaw   string
		baselineManifestRaw    string
		currentManifestRaw     string
		baselineWorkspaceRaw   string
		currentWorkspaceRaw    string
		staleReasonRaw         string
		driftSourceRaw         string
		consecutiveFailuresRaw int
		createdAtRaw           string
		updatedAtRaw           string
		auditedAtRaw           sql.NullString
		lastFailureAtRaw       sql.NullString
	)
	err := s.db.QueryRow(`SELECT tool_name, status, audit_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, audited_at, last_failure_at FROM tool_audit_records WHERE tool_name = ?`, toolName).Scan(&record.ToolName, &statusRaw, &record.AuditOutput, &record.Rationale, &artifactRefsRaw, &baselineFingerprintRaw, &currentFingerprintRaw, &baselineInstallRefRaw, &currentInstallRefRaw, &baselineManifestRaw, &currentManifestRaw, &baselineWorkspaceRaw, &currentWorkspaceRaw, &staleReasonRaw, &driftSourceRaw, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &auditedAtRaw, &lastFailureAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolAuditRecord{}, false, nil
	}
	if err != nil {
		return ToolAuditRecord{}, false, fmt.Errorf("load tool audit record: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record updated_at: %w", err)
	}
	record.Status = NormalizeToolAuditStatus(ToolAuditStatus(statusRaw))
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.ArtifactRefs = decodeRecordReferences(artifactRefsRaw)
	record.BaselineFingerprint = strings.TrimSpace(baselineFingerprintRaw)
	record.CurrentFingerprint = strings.TrimSpace(currentFingerprintRaw)
	record.BaselineInstallRef = strings.TrimSpace(baselineInstallRefRaw)
	record.CurrentInstallRef = strings.TrimSpace(currentInstallRefRaw)
	record.BaselineManifestHash = strings.TrimSpace(baselineManifestRaw)
	record.CurrentManifestHash = strings.TrimSpace(currentManifestRaw)
	record.BaselineWorkspaceFingerprint = strings.TrimSpace(baselineWorkspaceRaw)
	record.CurrentWorkspaceFingerprint = strings.TrimSpace(currentWorkspaceRaw)
	record.StaleReason = strings.TrimSpace(staleReasonRaw)
	record.DriftSource = ToolDriftSource(strings.TrimSpace(driftSourceRaw))
	record.ConsecutiveFailures = consecutiveFailuresRaw
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	if auditedAtRaw.Valid {
		record.AuditedAt, err = parseSQLiteTime(auditedAtRaw.String)
		if err != nil {
			return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record audited_at: %w", err)
		}
	}
	if lastFailureAtRaw.Valid {
		record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
		if err != nil {
			return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record last_failure_at: %w", err)
		}
	}
	return NormalizeToolAuditRecord(record), true, nil
}

func (s *SQLiteStore) ToolAuditRecords(status ToolAuditStatus, limit int) ([]ToolAuditRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	status = NormalizeToolAuditStatus(status)
	query := `SELECT tool_name, status, audit_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, audited_at, last_failure_at FROM tool_audit_records`
	args := make([]any, 0, 2)
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, string(status))
	}
	query += " ORDER BY updated_at DESC, tool_name ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool audit records: %w", err)
	}
	defer rows.Close()
	out := make([]ToolAuditRecord, 0, limit)
	for rows.Next() {
		var (
			record                 ToolAuditRecord
			statusRaw              string
			artifactRefsRaw        string
			baselineFingerprintRaw string
			currentFingerprintRaw  string
			baselineInstallRefRaw  string
			currentInstallRefRaw   string
			baselineManifestRaw    string
			currentManifestRaw     string
			baselineWorkspaceRaw   string
			currentWorkspaceRaw    string
			staleReasonRaw         string
			driftSourceRaw         string
			consecutiveFailuresRaw int
			createdAtRaw           string
			updatedAtRaw           string
			auditedAtRaw           sql.NullString
			lastFailureAtRaw       sql.NullString
		)
		if err := rows.Scan(&record.ToolName, &statusRaw, &record.AuditOutput, &record.Rationale, &artifactRefsRaw, &baselineFingerprintRaw, &currentFingerprintRaw, &baselineInstallRefRaw, &currentInstallRefRaw, &baselineManifestRaw, &currentManifestRaw, &baselineWorkspaceRaw, &currentWorkspaceRaw, &staleReasonRaw, &driftSourceRaw, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &auditedAtRaw, &lastFailureAtRaw); err != nil {
			return nil, fmt.Errorf("scan tool audit record: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool audit record created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool audit record updated_at: %w", err)
		}
		record.Status = NormalizeToolAuditStatus(ToolAuditStatus(statusRaw))
		record.Rationale = strings.TrimSpace(record.Rationale)
		record.ArtifactRefs = decodeRecordReferences(artifactRefsRaw)
		record.BaselineFingerprint = strings.TrimSpace(baselineFingerprintRaw)
		record.CurrentFingerprint = strings.TrimSpace(currentFingerprintRaw)
		record.BaselineInstallRef = strings.TrimSpace(baselineInstallRefRaw)
		record.CurrentInstallRef = strings.TrimSpace(currentInstallRefRaw)
		record.BaselineManifestHash = strings.TrimSpace(baselineManifestRaw)
		record.CurrentManifestHash = strings.TrimSpace(currentManifestRaw)
		record.BaselineWorkspaceFingerprint = strings.TrimSpace(baselineWorkspaceRaw)
		record.CurrentWorkspaceFingerprint = strings.TrimSpace(currentWorkspaceRaw)
		record.StaleReason = strings.TrimSpace(staleReasonRaw)
		record.DriftSource = ToolDriftSource(strings.TrimSpace(driftSourceRaw))
		record.ConsecutiveFailures = consecutiveFailuresRaw
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		if auditedAtRaw.Valid {
			record.AuditedAt, err = parseSQLiteTime(auditedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool audit record audited_at: %w", err)
			}
		}
		if lastFailureAtRaw.Valid {
			record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool audit record last_failure_at: %w", err)
			}
		}
		out = append(out, NormalizeToolAuditRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool audit records: %w", err)
	}
	return out, nil
}
