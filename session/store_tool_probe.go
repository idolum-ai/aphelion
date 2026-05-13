//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertToolProbeRecord(record ToolProbeRecord) (ToolProbeRecord, error) {
	record = NormalizeToolProbeRecord(record)
	if record.ToolName == "" {
		return ToolProbeRecord{}, fmt.Errorf("tool probe record tool_name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
			INSERT INTO tool_probe_records(tool_name, status, probe_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, probed_at, last_failure_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			status = excluded.status,
			probe_output = excluded.probe_output,
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
			probed_at = excluded.probed_at,
			last_failure_at = excluded.last_failure_at
	`, record.ToolName, string(record.Status), record.ProbeOutput, record.Rationale, encodeRecordReferences(record.ArtifactRefs), record.BaselineFingerprint, record.CurrentFingerprint, record.BaselineInstallRef, record.CurrentInstallRef, record.BaselineManifestHash, record.CurrentManifestHash, record.BaselineWorkspaceFingerprint, record.CurrentWorkspaceFingerprint, record.StaleReason, string(record.DriftSource), record.ConsecutiveFailures, createdAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano), nullableTimeRFC3339(record.ProbedAt), nullableTimeRFC3339(record.LastFailureAt)); err != nil {
		return ToolProbeRecord{}, fmt.Errorf("upsert tool probe record: %w", err)
	}
	stored, ok, err := s.ToolProbeRecord(record.ToolName)
	if err != nil {
		return ToolProbeRecord{}, err
	}
	if !ok {
		return ToolProbeRecord{}, fmt.Errorf("tool probe record %q not found after upsert", record.ToolName)
	}
	return stored, nil
}

func (s *SQLiteStore) ToolProbeRecord(toolName string) (ToolProbeRecord, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolProbeRecord{}, false, nil
	}
	var (
		record                 ToolProbeRecord
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
		probedAtRaw            sql.NullString
		lastFailureAtRaw       sql.NullString
	)
	err := s.db.QueryRow(`SELECT tool_name, status, probe_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, probed_at, last_failure_at FROM tool_probe_records WHERE tool_name = ?`, toolName).Scan(&record.ToolName, &statusRaw, &record.ProbeOutput, &record.Rationale, &artifactRefsRaw, &baselineFingerprintRaw, &currentFingerprintRaw, &baselineInstallRefRaw, &currentInstallRefRaw, &baselineManifestRaw, &currentManifestRaw, &baselineWorkspaceRaw, &currentWorkspaceRaw, &staleReasonRaw, &driftSourceRaw, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &probedAtRaw, &lastFailureAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolProbeRecord{}, false, nil
	}
	if err != nil {
		return ToolProbeRecord{}, false, fmt.Errorf("load tool probe record: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record updated_at: %w", err)
	}
	record.Status = NormalizeToolProbeStatus(ToolProbeStatus(statusRaw))
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
	if probedAtRaw.Valid {
		record.ProbedAt, err = parseSQLiteTime(probedAtRaw.String)
		if err != nil {
			return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record probed_at: %w", err)
		}
	}
	if lastFailureAtRaw.Valid {
		record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
		if err != nil {
			return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record last_failure_at: %w", err)
		}
	}
	return NormalizeToolProbeRecord(record), true, nil
}

func (s *SQLiteStore) ToolProbeRecords(status ToolProbeStatus, limit int) ([]ToolProbeRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	status = NormalizeToolProbeStatus(status)
	query := `SELECT tool_name, status, probe_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, probed_at, last_failure_at FROM tool_probe_records`
	args := make([]any, 0, 2)
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, string(status))
	}
	query += " ORDER BY updated_at DESC, tool_name ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool probe records: %w", err)
	}
	defer rows.Close()
	out := make([]ToolProbeRecord, 0, limit)
	for rows.Next() {
		var (
			record                 ToolProbeRecord
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
			probedAtRaw            sql.NullString
			lastFailureAtRaw       sql.NullString
		)
		if err := rows.Scan(&record.ToolName, &statusRaw, &record.ProbeOutput, &record.Rationale, &artifactRefsRaw, &baselineFingerprintRaw, &currentFingerprintRaw, &baselineInstallRefRaw, &currentInstallRefRaw, &baselineManifestRaw, &currentManifestRaw, &baselineWorkspaceRaw, &currentWorkspaceRaw, &staleReasonRaw, &driftSourceRaw, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &probedAtRaw, &lastFailureAtRaw); err != nil {
			return nil, fmt.Errorf("scan tool probe record: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool probe record created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool probe record updated_at: %w", err)
		}
		record.Status = NormalizeToolProbeStatus(ToolProbeStatus(statusRaw))
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
		if probedAtRaw.Valid {
			record.ProbedAt, err = parseSQLiteTime(probedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool probe record probed_at: %w", err)
			}
		}
		if lastFailureAtRaw.Valid {
			record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool probe record last_failure_at: %w", err)
			}
		}
		out = append(out, NormalizeToolProbeRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool probe records: %w", err)
	}
	return out, nil
}
