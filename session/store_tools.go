//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertRegisteredTool(record RegisteredTool) (RegisteredTool, error) {
	record = NormalizeRegisteredTool(record)
	if record.ToolName == "" {
		return RegisteredTool{}, fmt.Errorf("registered tool name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO registered_tools(tool_name, implementation_ref, registered, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			implementation_ref = excluded.implementation_ref,
			registered = excluded.registered,
			updated_at = excluded.updated_at
	`,
		record.ToolName,
		record.ImplementationRef,
		boolToInt(record.Registered),
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return RegisteredTool{}, fmt.Errorf("upsert registered tool: %w", err)
	}
	stored, ok, err := s.RegisteredTool(record.ToolName)
	if err != nil {
		return RegisteredTool{}, err
	}
	if !ok {
		return RegisteredTool{}, fmt.Errorf("registered tool %q not found after upsert", record.ToolName)
	}
	return stored, nil
}

func (s *SQLiteStore) RegisteredTool(toolName string) (RegisteredTool, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return RegisteredTool{}, false, nil
	}
	var (
		record       RegisteredTool
		registered   int
		createdAtRaw string
		updatedAtRaw string
	)
	err := s.db.QueryRow(`
		SELECT tool_name, implementation_ref, registered, created_at, updated_at
		FROM registered_tools
		WHERE tool_name = ?
	`, toolName).Scan(
		&record.ToolName,
		&record.ImplementationRef,
		&registered,
		&createdAtRaw,
		&updatedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredTool{}, false, nil
	}
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("load registered tool: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("parse registered tool created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("parse registered tool updated_at: %w", err)
	}
	record.Registered = registered != 0
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return NormalizeRegisteredTool(record), true, nil
}

func (s *SQLiteStore) RegisteredTools(limit int) ([]RegisteredTool, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT tool_name, implementation_ref, registered, created_at, updated_at
		FROM registered_tools
		ORDER BY updated_at DESC, tool_name ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query registered tools: %w", err)
	}
	defer rows.Close()

	out := make([]RegisteredTool, 0, limit)
	for rows.Next() {
		var (
			record       RegisteredTool
			registered   int
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(
			&record.ToolName,
			&record.ImplementationRef,
			&registered,
			&createdAtRaw,
			&updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan registered tool: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse registered tool created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse registered tool updated_at: %w", err)
		}
		record.Registered = registered != 0
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		out = append(out, NormalizeRegisteredTool(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registered tools: %w", err)
	}
	return out, nil
}

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

func nullableTimeRFC3339(ts time.Time) any {
	if ts.IsZero() {
		return nil
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

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
