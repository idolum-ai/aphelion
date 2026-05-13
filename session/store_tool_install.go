//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertToolInstallRecord(record ToolInstallRecord) (ToolInstallRecord, error) {
	record = NormalizeToolInstallRecord(record)
	if record.ToolName == "" {
		return ToolInstallRecord{}, fmt.Errorf("tool install record tool_name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO tool_install_records(tool_name, installer, install_ref, status, probe_status, probe_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, installed_at, last_probed_at, last_failure_at, attested_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			installer = excluded.installer,
			install_ref = excluded.install_ref,
			status = excluded.status,
			probe_status = excluded.probe_status,
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
			installed_at = excluded.installed_at,
			last_probed_at = excluded.last_probed_at,
			last_failure_at = excluded.last_failure_at,
			attested_at = excluded.attested_at
	`,
		record.ToolName,
		record.Installer,
		record.InstallRef,
		string(record.Status),
		string(record.ProbeStatus),
		record.ProbeOutput,
		record.Rationale,
		encodeRecordReferences(record.ArtifactRefs),
		record.BaselineFingerprint,
		record.CurrentFingerprint,
		record.BaselineInstallRef,
		record.CurrentInstallRef,
		record.BaselineManifestHash,
		record.CurrentManifestHash,
		record.BaselineWorkspaceFingerprint,
		record.CurrentWorkspaceFingerprint,
		record.StaleReason,
		string(record.DriftSource),
		record.ConsecutiveFailures,
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
		nullableTimeRFC3339(record.InstalledAt),
		nullableTimeRFC3339(record.LastProbedAt),
		nullableTimeRFC3339(record.LastFailureAt),
		nullableTimeRFC3339(record.AttestedAt),
	); err != nil {
		return ToolInstallRecord{}, fmt.Errorf("upsert tool install record: %w", err)
	}
	stored, ok, err := s.ToolInstallRecord(record.ToolName)
	if err != nil {
		return ToolInstallRecord{}, err
	}
	if !ok {
		return ToolInstallRecord{}, fmt.Errorf("tool install record %q not found after upsert", record.ToolName)
	}
	return stored, nil
}

func (s *SQLiteStore) ToolInstallRecord(toolName string) (ToolInstallRecord, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolInstallRecord{}, false, nil
	}
	var (
		record                 ToolInstallRecord
		statusRaw              string
		probeStatusRaw         string
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
		installedAtRaw         sql.NullString
		lastProbedAtRaw        sql.NullString
		lastFailureAtRaw       sql.NullString
		attestedAtRaw          sql.NullString
	)
	err := s.db.QueryRow(`
		SELECT tool_name, installer, install_ref, status, probe_status, probe_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, installed_at, last_probed_at, last_failure_at, attested_at
		FROM tool_install_records
		WHERE tool_name = ?
	`, toolName).Scan(
		&record.ToolName,
		&record.Installer,
		&record.InstallRef,
		&statusRaw,
		&probeStatusRaw,
		&record.ProbeOutput,
		&record.Rationale,
		&artifactRefsRaw,
		&baselineFingerprintRaw,
		&currentFingerprintRaw,
		&baselineInstallRefRaw,
		&currentInstallRefRaw,
		&baselineManifestRaw,
		&currentManifestRaw,
		&baselineWorkspaceRaw,
		&currentWorkspaceRaw,
		&staleReasonRaw,
		&driftSourceRaw,
		&consecutiveFailuresRaw,
		&createdAtRaw,
		&updatedAtRaw,
		&installedAtRaw,
		&lastProbedAtRaw,
		&lastFailureAtRaw,
		&attestedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolInstallRecord{}, false, nil
	}
	if err != nil {
		return ToolInstallRecord{}, false, fmt.Errorf("load tool install record: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record updated_at: %w", err)
	}
	record.Status = NormalizeToolInstallStatus(ToolInstallStatus(statusRaw))
	record.ProbeStatus = NormalizeToolProbeStatus(ToolProbeStatus(probeStatusRaw))
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
	if installedAtRaw.Valid {
		record.InstalledAt, err = parseSQLiteTime(installedAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record installed_at: %w", err)
		}
	}
	if lastProbedAtRaw.Valid {
		record.LastProbedAt, err = parseSQLiteTime(lastProbedAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record last_probed_at: %w", err)
		}
	}
	if lastFailureAtRaw.Valid {
		record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record last_failure_at: %w", err)
		}
	}
	if attestedAtRaw.Valid {
		record.AttestedAt, err = parseSQLiteTime(attestedAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record attested_at: %w", err)
		}
	}
	return NormalizeToolInstallRecord(record), true, nil
}

func (s *SQLiteStore) ToolInstallRecords(status ToolInstallStatus, limit int) ([]ToolInstallRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	status = NormalizeToolInstallStatus(status)
	query := `
		SELECT tool_name, installer, install_ref, status, probe_status, probe_output, rationale, artifact_refs_json, baseline_fingerprint, current_fingerprint, baseline_install_ref, current_install_ref, baseline_manifest_hash, current_manifest_hash, baseline_workspace_fingerprint, current_workspace_fingerprint, stale_reason, drift_source, consecutive_failures, created_at, updated_at, installed_at, last_probed_at, last_failure_at, attested_at
		FROM tool_install_records
	`
	args := make([]any, 0, 2)
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, string(status))
	}
	query += " ORDER BY updated_at DESC, tool_name ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool install records: %w", err)
	}
	defer rows.Close()
	out := make([]ToolInstallRecord, 0, limit)
	for rows.Next() {
		var (
			record                 ToolInstallRecord
			statusRaw              string
			probeStatusRaw         string
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
			installedAtRaw         sql.NullString
			lastProbedAtRaw        sql.NullString
			lastFailureAtRaw       sql.NullString
			attestedAtRaw          sql.NullString
		)
		if err := rows.Scan(&record.ToolName, &record.Installer, &record.InstallRef, &statusRaw, &probeStatusRaw, &record.ProbeOutput, &record.Rationale, &artifactRefsRaw, &baselineFingerprintRaw, &currentFingerprintRaw, &baselineInstallRefRaw, &currentInstallRefRaw, &baselineManifestRaw, &currentManifestRaw, &baselineWorkspaceRaw, &currentWorkspaceRaw, &staleReasonRaw, &driftSourceRaw, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &installedAtRaw, &lastProbedAtRaw, &lastFailureAtRaw, &attestedAtRaw); err != nil {
			return nil, fmt.Errorf("scan tool install record: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool install record created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool install record updated_at: %w", err)
		}
		record.Status = NormalizeToolInstallStatus(ToolInstallStatus(statusRaw))
		record.ProbeStatus = NormalizeToolProbeStatus(ToolProbeStatus(probeStatusRaw))
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
		if installedAtRaw.Valid {
			record.InstalledAt, err = parseSQLiteTime(installedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record installed_at: %w", err)
			}
		}
		if lastProbedAtRaw.Valid {
			record.LastProbedAt, err = parseSQLiteTime(lastProbedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record last_probed_at: %w", err)
			}
		}
		if lastFailureAtRaw.Valid {
			record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record last_failure_at: %w", err)
			}
		}
		if attestedAtRaw.Valid {
			record.AttestedAt, err = parseSQLiteTime(attestedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record attested_at: %w", err)
			}
		}
		out = append(out, NormalizeToolInstallRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool install records: %w", err)
	}
	return out, nil
}
