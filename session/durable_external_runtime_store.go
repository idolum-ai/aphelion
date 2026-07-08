//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type DurableChildRuntimeSpecStatus string
type DurableChildWorkAgreementVersionStatus string
type DurableChildLeaseMaterializationStatus string
type DurableChildWorkAgreementAmendmentStatus string

const (
	DurableChildRuntimeSpecStatusPending  DurableChildRuntimeSpecStatus = "pending"
	DurableChildRuntimeSpecStatusVerified DurableChildRuntimeSpecStatus = "verified"
	DurableChildRuntimeSpecStatusStale    DurableChildRuntimeSpecStatus = "stale"
	DurableChildRuntimeSpecStatusRevoked  DurableChildRuntimeSpecStatus = "revoked"
	DurableChildRuntimeSpecStatusFailed   DurableChildRuntimeSpecStatus = "failed"

	DurableChildWorkAgreementVersionStatusProposed   DurableChildWorkAgreementVersionStatus = "proposed"
	DurableChildWorkAgreementVersionStatusActive     DurableChildWorkAgreementVersionStatus = "active"
	DurableChildWorkAgreementVersionStatusRejected   DurableChildWorkAgreementVersionStatus = "rejected"
	DurableChildWorkAgreementVersionStatusSuperseded DurableChildWorkAgreementVersionStatus = "superseded"
	DurableChildWorkAgreementVersionStatusRevoked    DurableChildWorkAgreementVersionStatus = "revoked"

	DurableChildLeaseMaterializationStatusActive   DurableChildLeaseMaterializationStatus = "active"
	DurableChildLeaseMaterializationStatusConsumed DurableChildLeaseMaterializationStatus = "consumed"
	DurableChildLeaseMaterializationStatusRevoked  DurableChildLeaseMaterializationStatus = "revoked"
	DurableChildLeaseMaterializationStatusExpired  DurableChildLeaseMaterializationStatus = "expired"

	DurableChildWorkAgreementAmendmentStatusProposed   DurableChildWorkAgreementAmendmentStatus = "proposed"
	DurableChildWorkAgreementAmendmentStatusApproved   DurableChildWorkAgreementAmendmentStatus = "approved"
	DurableChildWorkAgreementAmendmentStatusRejected   DurableChildWorkAgreementAmendmentStatus = "rejected"
	DurableChildWorkAgreementAmendmentStatusSuperseded DurableChildWorkAgreementAmendmentStatus = "superseded"
)

type DurableChildRuntimeSpecRecord struct {
	SpecID              string
	AgentID             string
	SpecHash            string
	RuntimeKind         string
	RuntimeMode         string
	SourceRef           string
	Spec                core.DurableExternalRuntimeSpec
	InstallStatus       DurableChildRuntimeSpecStatus
	ProbeStatus         string
	DriftStatus         string
	SourceRequestID     string
	SourceReviewEventID int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	VerifiedAt          time.Time
	StaleAt             time.Time
}

type DurableChildWorkAgreementVersionRecord struct {
	AgreementID         string
	Version             int
	AgentID             string
	Status              DurableChildWorkAgreementVersionStatus
	AuthorityPrincipal  string
	ReviewPrincipal     string
	RuntimeKind         string
	AgreementHash       string
	Agreement           core.WorkAgreement
	SourceRequestID     string
	SourceReviewEventID int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ActivatedAt         time.Time
	SupersededAt        time.Time
	RevokedAt           time.Time
}

func (r DurableChildWorkAgreementVersionRecord) AgreementForMaterialization() core.WorkAgreement {
	agreement := core.NormalizeWorkAgreement(r.Agreement)
	if r.Status == DurableChildWorkAgreementVersionStatusActive {
		agreement.Status = "active"
	} else {
		agreement.Status = ""
	}
	return agreement
}

type DurableChildConditionalGrantRecord struct {
	AgreementID      string
	AgreementVersion int
	GrantID          string
	AgentID          string
	Status           string
	Capability       string
	Tool             string
	Actions          []string
	Triggers         []string
	CredentialScope  string
	Grant            core.ConditionalGrant
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type DurableChildLeaseMaterializationRecord struct {
	LeaseID                          string
	MaterializationID                string
	AgentID                          string
	AgreementID                      string
	AgreementVersion                 int
	ConditionalGrantID               string
	ConditionalGrantAgreementVersion int
	Capability                       string
	LeaseKind                        string
	ReviewRoute                      string
	RuntimeSpecHash                  string
	Trigger                          string
	SingleUse                        bool
	Status                           DurableChildLeaseMaterializationStatus
	Materialization                  core.LeaseMaterialization
	CreatedAt                        time.Time
	ExpiresAt                        time.Time
	ConsumedAt                       time.Time
	RevokedAt                        time.Time
}

type DurableChildWorkAgreementAmendmentRecord struct {
	AmendmentID         string
	AgreementID         string
	FromVersion         int
	ProposedVersion     int
	ProposedBy          string
	Status              DurableChildWorkAgreementAmendmentStatus
	ChangeClass         []string
	Amendment           core.WorkAgreementAmendment
	SourceRequestID     string
	SourceReviewEventID int64
	ResultReviewEventID int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ResolvedAt          time.Time
}

type DurableExternalRuntimeWorkAgreementDraftInput struct {
	RuntimeSpec         core.DurableExternalRuntimeSpec
	WorkAgreement       core.WorkAgreement
	ConditionalGrants   []core.ConditionalGrant
	SourceRequestID     string
	SourceReviewEventID int64
	CreatedAt           time.Time
}

type DurableExternalRuntimeWorkAgreementDraft struct {
	RuntimeSpec       DurableChildRuntimeSpecRecord
	WorkAgreement     DurableChildWorkAgreementVersionRecord
	ConditionalGrants []DurableChildConditionalGrantRecord
}

type durableExternalRuntimeSQL interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *SQLiteStore) UpsertDurableChildRuntimeSpec(record DurableChildRuntimeSpecRecord) (DurableChildRuntimeSpecRecord, error) {
	normalized, err := upsertDurableChildRuntimeSpecSQL(s.db, record)
	if err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	stored, ok, err := s.DurableChildRuntimeSpec(normalized.SpecID)
	if err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	if !ok {
		return DurableChildRuntimeSpecRecord{}, fmt.Errorf("durable child runtime spec %q not found after upsert", normalized.SpecID)
	}
	return stored, nil
}

func upsertDurableChildRuntimeSpecSQL(db durableExternalRuntimeSQL, record DurableChildRuntimeSpecRecord) (DurableChildRuntimeSpecRecord, error) {
	normalized, specJSON, err := normalizeDurableChildRuntimeSpecRecord(record)
	if err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	if err := ensureDurableChildRuntimeSpecContentUnchanged(db, normalized.SpecID, normalized.SpecHash, specJSON); err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	if _, err := db.Exec(`
		INSERT INTO durable_child_runtime_specs(
			spec_id, agent_id, spec_hash, runtime_kind, runtime_mode, source_ref, spec_json,
			install_status, probe_status, drift_status, source_request_id, source_review_event_id,
			created_at, updated_at, verified_at, stale_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(spec_id) DO UPDATE SET
			install_status = CASE
				WHEN excluded.install_status = 'pending' THEN durable_child_runtime_specs.install_status
				ELSE excluded.install_status
			END,
			probe_status = CASE
				WHEN excluded.install_status = 'pending' THEN durable_child_runtime_specs.probe_status
				ELSE excluded.probe_status
			END,
			drift_status = CASE
				WHEN excluded.install_status = 'pending' THEN durable_child_runtime_specs.drift_status
				ELSE excluded.drift_status
			END,
			source_request_id = excluded.source_request_id,
			source_review_event_id = excluded.source_review_event_id,
			updated_at = excluded.updated_at,
			verified_at = CASE
				WHEN excluded.install_status = 'pending' THEN durable_child_runtime_specs.verified_at
				ELSE excluded.verified_at
			END,
			stale_at = CASE
				WHEN excluded.install_status = 'pending' THEN durable_child_runtime_specs.stale_at
				ELSE excluded.stale_at
			END
	`, normalized.SpecID, normalized.AgentID, normalized.SpecHash, normalized.RuntimeKind, normalized.RuntimeMode, normalized.SourceRef, specJSON,
		string(normalized.InstallStatus), normalized.ProbeStatus, normalized.DriftStatus, normalized.SourceRequestID, normalized.SourceReviewEventID,
		formatSQLiteTime(normalized.CreatedAt), formatSQLiteTime(normalized.UpdatedAt), nullableSQLiteTime(normalized.VerifiedAt), nullableSQLiteTime(normalized.StaleAt)); err != nil {
		return DurableChildRuntimeSpecRecord{}, fmt.Errorf("upsert durable child runtime spec: %w", err)
	}
	return normalized, nil
}

func (s *SQLiteStore) DurableChildRuntimeSpec(specID string) (DurableChildRuntimeSpecRecord, bool, error) {
	return durableChildRuntimeSpecSQL(s.db, specID)
}

func durableChildRuntimeSpecSQL(db durableExternalRuntimeSQL, specID string) (DurableChildRuntimeSpecRecord, bool, error) {
	specID = strings.TrimSpace(specID)
	if specID == "" {
		return DurableChildRuntimeSpecRecord{}, false, nil
	}
	row := db.QueryRow(durableChildRuntimeSpecSelect()+` WHERE spec_id = ?`, specID)
	record, err := scanDurableChildRuntimeSpec(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildRuntimeSpecRecord{}, false, nil
	}
	if err != nil {
		return DurableChildRuntimeSpecRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) DurableChildRuntimeSpecByHash(agentID string, specHash string) (DurableChildRuntimeSpecRecord, bool, error) {
	agentID = strings.TrimSpace(agentID)
	specHash = strings.TrimSpace(specHash)
	if agentID == "" || specHash == "" {
		return DurableChildRuntimeSpecRecord{}, false, nil
	}
	row := s.db.QueryRow(durableChildRuntimeSpecSelect()+` WHERE agent_id = ? AND spec_hash = ?`, agentID, specHash)
	record, err := scanDurableChildRuntimeSpec(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildRuntimeSpecRecord{}, false, nil
	}
	if err != nil {
		return DurableChildRuntimeSpecRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) UpsertDurableChildWorkAgreementVersion(record DurableChildWorkAgreementVersionRecord) (DurableChildWorkAgreementVersionRecord, error) {
	normalized, err := upsertDurableChildWorkAgreementVersionSQL(s.db, record)
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	return s.DurableChildWorkAgreementVersion(normalized.AgreementID, normalized.Version)
}

func upsertDurableChildWorkAgreementVersionSQL(db durableExternalRuntimeSQL, record DurableChildWorkAgreementVersionRecord) (DurableChildWorkAgreementVersionRecord, error) {
	normalized, agreementJSON, err := normalizeDurableChildWorkAgreementVersionRecord(record)
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	if err := ensureDurableChildWorkAgreementContentUnchanged(db, normalized.AgreementID, normalized.Version, normalized.AgreementHash, agreementJSON); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	if _, err := db.Exec(`
		INSERT INTO durable_child_work_agreement_versions(
			agreement_id, version, agent_id, status, authority_principal, review_principal,
			runtime_kind, agreement_hash, agreement_json, source_request_id, source_review_event_id,
			created_at, updated_at, activated_at, superseded_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agreement_id, version) DO UPDATE SET
			updated_at = excluded.updated_at
	`, normalized.AgreementID, normalized.Version, normalized.AgentID, string(normalized.Status), normalized.AuthorityPrincipal, normalized.ReviewPrincipal,
		normalized.RuntimeKind, normalized.AgreementHash, agreementJSON, normalized.SourceRequestID, normalized.SourceReviewEventID,
		formatSQLiteTime(normalized.CreatedAt), formatSQLiteTime(normalized.UpdatedAt), nullableSQLiteTime(normalized.ActivatedAt), nullableSQLiteTime(normalized.SupersededAt), nullableSQLiteTime(normalized.RevokedAt)); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, fmt.Errorf("upsert durable child work agreement version: %w", err)
	}
	return normalized, nil
}

func (s *SQLiteStore) DurableChildWorkAgreementVersion(agreementID string, version int) (DurableChildWorkAgreementVersionRecord, error) {
	return durableChildWorkAgreementVersionSQL(s.db, agreementID, version)
}

func durableChildWorkAgreementVersionSQL(db durableExternalRuntimeSQL, agreementID string, version int) (DurableChildWorkAgreementVersionRecord, error) {
	agreementID = strings.TrimSpace(agreementID)
	if agreementID == "" || version <= 0 {
		return DurableChildWorkAgreementVersionRecord{}, sql.ErrNoRows
	}
	return scanDurableChildWorkAgreementVersion(db.QueryRow(durableChildWorkAgreementVersionSelect()+` WHERE agreement_id = ? AND version = ?`, agreementID, version))
}

func (s *SQLiteStore) ActiveDurableChildWorkAgreementVersion(agentID string, agreementID string) (DurableChildWorkAgreementVersionRecord, bool, error) {
	agentID = strings.TrimSpace(agentID)
	agreementID = strings.TrimSpace(agreementID)
	if agentID == "" || agreementID == "" {
		return DurableChildWorkAgreementVersionRecord{}, false, nil
	}
	record, err := scanDurableChildWorkAgreementVersion(s.db.QueryRow(durableChildWorkAgreementVersionSelect()+`
		WHERE agent_id = ? AND agreement_id = ? AND status = 'active'
		ORDER BY version DESC
		LIMIT 1
	`, agentID, agreementID))
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildWorkAgreementVersionRecord{}, false, nil
	}
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) UpsertDurableChildConditionalGrant(record DurableChildConditionalGrantRecord) (DurableChildConditionalGrantRecord, error) {
	normalized, err := upsertDurableChildConditionalGrantSQL(s.db, record)
	if err != nil {
		return DurableChildConditionalGrantRecord{}, err
	}
	return s.DurableChildConditionalGrant(normalized.AgreementID, normalized.AgreementVersion, normalized.GrantID)
}

func upsertDurableChildConditionalGrantSQL(db durableExternalRuntimeSQL, record DurableChildConditionalGrantRecord) (DurableChildConditionalGrantRecord, error) {
	normalized, grantJSON, actionsJSON, triggersJSON, err := normalizeDurableChildConditionalGrantRecord(record)
	if err != nil {
		return DurableChildConditionalGrantRecord{}, err
	}
	if err := ensureDurableChildConditionalGrantContentUnchanged(db, normalized.AgreementID, normalized.AgreementVersion, normalized.GrantID, grantJSON); err != nil {
		return DurableChildConditionalGrantRecord{}, err
	}
	if _, err := db.Exec(`
		INSERT INTO durable_child_conditional_grants(
			agreement_id, agreement_version, grant_id, agent_id, status, capability, tool,
			actions_json, triggers_json, credential_scope, grant_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agreement_id, agreement_version, grant_id) DO UPDATE SET
			status = excluded.status,
			updated_at = excluded.updated_at
	`, normalized.AgreementID, normalized.AgreementVersion, normalized.GrantID, normalized.AgentID, normalized.Status, normalized.Capability, normalized.Tool,
		actionsJSON, triggersJSON, normalized.CredentialScope, grantJSON, formatSQLiteTime(normalized.CreatedAt), formatSQLiteTime(normalized.UpdatedAt)); err != nil {
		return DurableChildConditionalGrantRecord{}, fmt.Errorf("upsert durable child conditional grant: %w", err)
	}
	return normalized, nil
}

func (s *SQLiteStore) DurableChildConditionalGrant(agreementID string, version int, grantID string) (DurableChildConditionalGrantRecord, error) {
	return durableChildConditionalGrantSQL(s.db, agreementID, version, grantID)
}

func durableChildConditionalGrantSQL(db durableExternalRuntimeSQL, agreementID string, version int, grantID string) (DurableChildConditionalGrantRecord, error) {
	agreementID = strings.TrimSpace(agreementID)
	grantID = strings.TrimSpace(grantID)
	if agreementID == "" || version <= 0 || grantID == "" {
		return DurableChildConditionalGrantRecord{}, sql.ErrNoRows
	}
	return scanDurableChildConditionalGrant(db.QueryRow(durableChildConditionalGrantSelect()+`
		WHERE agreement_id = ? AND agreement_version = ? AND grant_id = ?
	`, agreementID, version, grantID))
}

func (s *SQLiteStore) DurableChildConditionalGrantsForWorkAgreement(agreementID string, version int) ([]DurableChildConditionalGrantRecord, error) {
	agreementID = strings.TrimSpace(agreementID)
	if agreementID == "" || version <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(durableChildConditionalGrantSelect()+`
		WHERE agreement_id = ? AND agreement_version = ?
		ORDER BY grant_id ASC
	`, agreementID, version)
	if err != nil {
		return nil, fmt.Errorf("query durable child conditional grants: %w", err)
	}
	defer rows.Close()
	return scanDurableChildConditionalGrantRows(rows)
}

func (s *SQLiteStore) InsertDurableChildLeaseMaterialization(materialization core.LeaseMaterialization) ([]DurableChildLeaseMaterializationRecord, error) {
	materialization = core.NormalizeLeaseMaterialization(materialization)
	if err := core.ValidateLeaseMaterialization(materialization); err != nil {
		return nil, err
	}
	materializationJSON, err := marshalExternalRuntimeJSON(materialization)
	if err != nil {
		return nil, err
	}
	now := nonZeroTimeOrNow(materialization.CreatedAt, time.Now().UTC()).UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin durable child lease materialization tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	records := make([]DurableChildLeaseMaterializationRecord, 0, len(materialization.IssuedLeases))
	for _, lease := range materialization.IssuedLeases {
		agreementRecord, grantRecord, err := validateLeaseMaterializationFenceForInsertTx(tx, materialization, lease)
		if err != nil {
			return nil, err
		}
		record := DurableChildLeaseMaterializationRecord{
			LeaseID:                          lease.LeaseID,
			MaterializationID:                materialization.ID,
			AgentID:                          materialization.AgentID,
			AgreementID:                      materialization.WorkAgreementID,
			AgreementVersion:                 materialization.WorkAgreementVersion,
			ConditionalGrantID:               lease.ConditionalGrantID,
			ConditionalGrantAgreementVersion: lease.ConditionalGrantAgreementVersion,
			Capability:                       lease.Capability,
			LeaseKind:                        lease.LeaseKind,
			ReviewRoute:                      lease.ReviewRoute,
			RuntimeSpecHash:                  materialization.RuntimeSpecHash,
			Trigger:                          materialization.MatchedConditions.Trigger,
			SingleUse:                        lease.SingleUse,
			Status:                           DurableChildLeaseMaterializationStatusActive,
			Materialization:                  materialization,
			CreatedAt:                        now,
			ExpiresAt:                        lease.ExpiresAt,
		}
		existing, ok, err := durableChildLeaseMaterializationSQL(tx, record.LeaseID)
		if err != nil {
			return nil, err
		}
		if ok {
			existingJSON, err := marshalExternalRuntimeJSON(core.NormalizeLeaseMaterialization(existing.Materialization))
			if err != nil {
				return nil, err
			}
			if existingJSON != materializationJSON {
				return nil, fmt.Errorf("durable child lease materialization %q already exists with different contract", record.LeaseID)
			}
			records = append(records, existing)
			continue
		}
		if agreementRecord.Status != DurableChildWorkAgreementVersionStatusActive {
			return nil, fmt.Errorf("lease materialization agreement %s/%d is not active", materialization.WorkAgreementID, materialization.WorkAgreementVersion)
		}
		if grantRecord.Status != "" && grantRecord.Status != "active" {
			return nil, fmt.Errorf("lease materialization conditional grant %s/%d/%s is not active", grantRecord.AgreementID, grantRecord.AgreementVersion, grantRecord.GrantID)
		}
		if _, err := tx.Exec(`
			INSERT INTO durable_child_lease_materializations(
				lease_id, materialization_id, agent_id, agreement_id, agreement_version,
				conditional_grant_id, conditional_grant_agreement_version, capability, lease_kind,
				review_route, runtime_spec_hash, trigger, single_use, status,
				materialization_json, created_at, expires_at, consumed_at, revoked_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.LeaseID, record.MaterializationID, record.AgentID, record.AgreementID, record.AgreementVersion,
			record.ConditionalGrantID, record.ConditionalGrantAgreementVersion, record.Capability, record.LeaseKind,
			record.ReviewRoute, record.RuntimeSpecHash, record.Trigger, boolInt(record.SingleUse), string(record.Status),
			materializationJSON, formatSQLiteTime(record.CreatedAt), nullableSQLiteTime(record.ExpiresAt), nil, nil); err != nil {
			return nil, fmt.Errorf("insert durable child lease materialization: %w", err)
		}
		records = append(records, record)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit durable child lease materialization tx: %w", err)
	}
	return records, nil
}

func (s *SQLiteStore) DurableChildLeaseMaterialization(leaseID string) (DurableChildLeaseMaterializationRecord, bool, error) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return DurableChildLeaseMaterializationRecord{}, false, nil
	}
	return durableChildLeaseMaterializationSQL(s.db, leaseID)
}

func durableChildLeaseMaterializationSQL(db durableExternalRuntimeSQL, leaseID string) (DurableChildLeaseMaterializationRecord, bool, error) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return DurableChildLeaseMaterializationRecord{}, false, nil
	}
	record, err := scanDurableChildLeaseMaterialization(db.QueryRow(durableChildLeaseMaterializationSelect()+` WHERE lease_id = ?`, leaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildLeaseMaterializationRecord{}, false, nil
	}
	if err != nil {
		return DurableChildLeaseMaterializationRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) ConsumeDurableChildLease(leaseID string, at time.Time) (DurableChildLeaseMaterializationRecord, bool, error) {
	return s.transitionDurableChildLease(leaseID, DurableChildLeaseMaterializationStatusConsumed, at)
}

func (s *SQLiteStore) RevokeDurableChildLease(leaseID string, at time.Time) (DurableChildLeaseMaterializationRecord, bool, error) {
	return s.transitionDurableChildLease(leaseID, DurableChildLeaseMaterializationStatusRevoked, at)
}

func (s *SQLiteStore) RevokeDurableChildWorkAgreementVersion(agentID string, agreementID string, version int, at time.Time) (DurableChildWorkAgreementVersionRecord, bool, int64, error) {
	agentID = strings.TrimSpace(agentID)
	agreementID = strings.TrimSpace(agreementID)
	if agentID == "" || agreementID == "" || version <= 0 {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, sql.ErrNoRows
	}
	at = nonZeroTimeOrNow(at, time.Now().UTC()).UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, fmt.Errorf("begin work agreement revocation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	record, changed, revokedLeases, err := revokeDurableChildWorkAgreementVersionTx(tx, agentID, agreementID, version, at)
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, fmt.Errorf("commit work agreement revocation tx: %w", err)
	}
	return record, changed, revokedLeases, nil
}

func (s *SQLiteStore) UpsertDurableChildWorkAgreementAmendment(record DurableChildWorkAgreementAmendmentRecord) (DurableChildWorkAgreementAmendmentRecord, error) {
	normalized, amendmentJSON, changeClassJSON, err := normalizeDurableChildWorkAgreementAmendmentRecord(record)
	if err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, err
	}
	if _, err := s.db.Exec(`
		INSERT INTO durable_child_work_agreement_amendments(
			amendment_id, agreement_id, from_version, proposed_version, proposed_by,
			status, change_class_json, amendment_json, source_request_id, source_review_event_id,
			result_review_event_id, created_at, updated_at, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(amendment_id) DO UPDATE SET
			agreement_id = excluded.agreement_id,
			from_version = excluded.from_version,
			proposed_version = excluded.proposed_version,
			proposed_by = excluded.proposed_by,
			status = excluded.status,
			change_class_json = excluded.change_class_json,
			amendment_json = excluded.amendment_json,
			source_request_id = excluded.source_request_id,
			source_review_event_id = excluded.source_review_event_id,
			result_review_event_id = excluded.result_review_event_id,
			updated_at = excluded.updated_at,
			resolved_at = excluded.resolved_at
	`, normalized.AmendmentID, normalized.AgreementID, normalized.FromVersion, normalized.ProposedVersion, normalized.ProposedBy,
		string(normalized.Status), changeClassJSON, amendmentJSON, normalized.SourceRequestID, normalized.SourceReviewEventID,
		normalized.ResultReviewEventID, formatSQLiteTime(normalized.CreatedAt), formatSQLiteTime(normalized.UpdatedAt), nullableSQLiteTime(normalized.ResolvedAt)); err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, fmt.Errorf("upsert durable child work agreement amendment: %w", err)
	}
	return s.DurableChildWorkAgreementAmendment(normalized.AmendmentID)
}

func (s *SQLiteStore) DurableChildWorkAgreementAmendment(amendmentID string) (DurableChildWorkAgreementAmendmentRecord, error) {
	amendmentID = strings.TrimSpace(amendmentID)
	if amendmentID == "" {
		return DurableChildWorkAgreementAmendmentRecord{}, sql.ErrNoRows
	}
	return scanDurableChildWorkAgreementAmendment(s.db.QueryRow(durableChildWorkAgreementAmendmentSelect()+` WHERE amendment_id = ?`, amendmentID))
}

func (s *SQLiteStore) CreateDurableExternalRuntimeWorkAgreementDraft(input DurableExternalRuntimeWorkAgreementDraftInput) (DurableExternalRuntimeWorkAgreementDraft, error) {
	now := nonZeroTimeOrNow(input.CreatedAt, time.Now().UTC()).UTC()
	runtimeInput := DurableChildRuntimeSpecRecord{
		AgentID:             strings.TrimSpace(input.WorkAgreement.AgentID),
		Spec:                input.RuntimeSpec,
		InstallStatus:       DurableChildRuntimeSpecStatusPending,
		SourceRequestID:     input.SourceRequestID,
		SourceReviewEventID: input.SourceReviewEventID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	runtimeInput, _, err := normalizeDurableChildRuntimeSpecRecord(runtimeInput)
	if err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, err
	}
	agreementInput := DurableChildWorkAgreementVersionRecord{
		Agreement:           input.WorkAgreement,
		Status:              DurableChildWorkAgreementVersionStatusProposed,
		SourceRequestID:     input.SourceRequestID,
		SourceReviewEventID: input.SourceReviewEventID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	agreementInput, _, err = normalizeDurableChildWorkAgreementVersionRecord(agreementInput)
	if err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, err
	}
	grantInputs := make([]DurableChildConditionalGrantRecord, 0, len(input.ConditionalGrants))
	for _, grant := range input.ConditionalGrants {
		grantInput := DurableChildConditionalGrantRecord{
			AgentID:   agreementInput.AgentID,
			Grant:     grant,
			CreatedAt: now,
			UpdatedAt: now,
		}
		grantInput, _, _, _, err = normalizeDurableChildConditionalGrantRecord(grantInput)
		if err != nil {
			return DurableExternalRuntimeWorkAgreementDraft{}, err
		}
		grantInputs = append(grantInputs, grantInput)
	}
	if err := validateDraftGrantSet(agreementInput, grantInputs); err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, fmt.Errorf("begin durable external runtime draft tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := upsertDurableChildRuntimeSpecSQL(tx, runtimeInput); err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, err
	}
	runtimeSpec, ok, err := durableChildRuntimeSpecSQL(tx, runtimeInput.SpecID)
	if err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, err
	}
	if !ok {
		return DurableExternalRuntimeWorkAgreementDraft{}, fmt.Errorf("durable child runtime spec %q not found after draft upsert", runtimeInput.SpecID)
	}
	if _, err := upsertDurableChildWorkAgreementVersionSQL(tx, agreementInput); err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, err
	}
	agreement, err := durableChildWorkAgreementVersionSQL(tx, agreementInput.AgreementID, agreementInput.Version)
	if err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, err
	}
	grants := make([]DurableChildConditionalGrantRecord, 0, len(grantInputs))
	for _, grantInput := range grantInputs {
		if _, err := upsertDurableChildConditionalGrantSQL(tx, grantInput); err != nil {
			return DurableExternalRuntimeWorkAgreementDraft{}, err
		}
		stored, err := durableChildConditionalGrantSQL(tx, grantInput.AgreementID, grantInput.AgreementVersion, grantInput.GrantID)
		if err != nil {
			return DurableExternalRuntimeWorkAgreementDraft{}, err
		}
		grants = append(grants, stored)
	}
	if err := tx.Commit(); err != nil {
		return DurableExternalRuntimeWorkAgreementDraft{}, fmt.Errorf("commit durable external runtime draft tx: %w", err)
	}
	return DurableExternalRuntimeWorkAgreementDraft{RuntimeSpec: runtimeSpec, WorkAgreement: agreement, ConditionalGrants: grants}, nil
}

func (s *SQLiteStore) MaterializeActiveWorkAgreementLeases(agentID string, agreementID string, trigger string, runtimeSpecHash string, now time.Time) ([]DurableChildLeaseMaterializationRecord, error) {
	agreement, ok, err := s.ActiveDurableChildWorkAgreementVersion(agentID, agreementID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("active work agreement %q for agent %q not found", agreementID, agentID)
	}
	runtimeSpec, ok, err := s.DurableChildRuntimeSpecByHash(agentID, runtimeSpecHash)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("runtime spec %q for agent %q not found", runtimeSpecHash, agentID)
	}
	if err := validateRuntimeSpecReadyForWorkAgreement(runtimeSpec, agreement); err != nil {
		return nil, err
	}
	grantRecords, err := s.DurableChildConditionalGrantsForWorkAgreement(agreement.AgreementID, agreement.Version)
	if err != nil {
		return nil, err
	}
	grants := make([]core.ConditionalGrant, 0, len(grantRecords))
	for _, record := range grantRecords {
		grants = append(grants, record.Grant)
	}
	materialization, err := core.BuildCandidateWorkAgreementLeases(agreement.AgreementForMaterialization(), grants, trigger, runtimeSpecHash, now)
	if err != nil {
		return nil, err
	}
	return s.InsertDurableChildLeaseMaterialization(materialization)
}

func (s *SQLiteStore) UpdateDurableChildWorkAgreementStatusForRequest(requestID string, reviewStatus CapabilityReviewStatus) error {
	requestID = strings.TrimSpace(requestID)
	next := DurableChildWorkAgreementVersionStatusFromCapabilityReview(reviewStatus)
	if requestID == "" || next == "" {
		return nil
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin work agreement status transition tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateWorkAgreementStatusForRequestTx(tx, requestID, next, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit work agreement status transition tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateDurableChildWorkAgreementAmendmentStatusForRequest(requestID string, reviewStatus CapabilityReviewStatus) error {
	requestID = strings.TrimSpace(requestID)
	next := DurableChildWorkAgreementAmendmentStatusFromCapabilityReview(reviewStatus)
	if requestID == "" || next == "" {
		return nil
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin work agreement amendment transition tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateWorkAgreementAmendmentStatusForRequestTx(tx, requestID, next, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit work agreement amendment transition tx: %w", err)
	}
	return nil
}

func DurableChildWorkAgreementVersionStatusFromCapabilityReview(status CapabilityReviewStatus) DurableChildWorkAgreementVersionStatus {
	switch NormalizeCapabilityReviewStatus(status) {
	case CapabilityReviewStatusApproved:
		return DurableChildWorkAgreementVersionStatusActive
	case CapabilityReviewStatusRejected:
		return DurableChildWorkAgreementVersionStatusRejected
	case CapabilityReviewStatusProposed, CapabilityReviewStatusParentApproved:
		return DurableChildWorkAgreementVersionStatusProposed
	default:
		return ""
	}
}

func DurableChildWorkAgreementAmendmentStatusFromCapabilityReview(status CapabilityReviewStatus) DurableChildWorkAgreementAmendmentStatus {
	switch NormalizeCapabilityReviewStatus(status) {
	case CapabilityReviewStatusApproved:
		return DurableChildWorkAgreementAmendmentStatusApproved
	case CapabilityReviewStatusRejected:
		return DurableChildWorkAgreementAmendmentStatusRejected
	case CapabilityReviewStatusProposed, CapabilityReviewStatusParentApproved:
		return DurableChildWorkAgreementAmendmentStatusProposed
	default:
		return ""
	}
}

func normalizeDurableChildRuntimeSpecRecord(record DurableChildRuntimeSpecRecord) (DurableChildRuntimeSpecRecord, string, error) {
	record.Spec = core.NormalizeDurableExternalRuntimeSpec(record.Spec)
	if err := core.ValidateDurableExternalRuntimeSpec(record.Spec); err != nil {
		return DurableChildRuntimeSpecRecord{}, "", err
	}
	record.AgentID = strings.TrimSpace(record.AgentID)
	if record.AgentID == "" {
		return DurableChildRuntimeSpecRecord{}, "", fmt.Errorf("durable child runtime spec requires agent_id")
	}
	hash, err := core.StableExternalRuntimeContractHash(record.Spec)
	if err != nil {
		return DurableChildRuntimeSpecRecord{}, "", err
	}
	record.SpecHash = firstNonEmptyStore(strings.TrimSpace(record.SpecHash), hash)
	if record.SpecHash != hash {
		return DurableChildRuntimeSpecRecord{}, "", fmt.Errorf("durable child runtime spec hash mismatch")
	}
	record.RuntimeKind = record.Spec.Kind
	record.RuntimeMode = record.Spec.Mode
	record.SourceRef = runtimeSourceRef(record.Spec.Source)
	record.SpecID = firstNonEmptyStore(strings.TrimSpace(record.SpecID), externalRuntimeStoreID("runtime_spec", record.AgentID, record.SpecHash))
	record.InstallStatus = NormalizeDurableChildRuntimeSpecStatus(record.InstallStatus)
	if record.InstallStatus == "" {
		record.InstallStatus = DurableChildRuntimeSpecStatusPending
	}
	record.ProbeStatus = normalizeEnumValue(record.ProbeStatus)
	record.DriftStatus = normalizeEnumValue(record.DriftStatus)
	record.SourceRequestID = strings.TrimSpace(record.SourceRequestID)
	now := time.Now().UTC()
	record.CreatedAt = nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	record.VerifiedAt = normalizeOptionalTime(record.VerifiedAt)
	record.StaleAt = normalizeOptionalTime(record.StaleAt)
	specJSON, err := marshalExternalRuntimeJSON(record.Spec)
	return record, specJSON, err
}

func normalizeDurableChildWorkAgreementVersionRecord(record DurableChildWorkAgreementVersionRecord) (DurableChildWorkAgreementVersionRecord, string, error) {
	record.Agreement = workAgreementForStorage(record.Agreement)
	if err := core.ValidateWorkAgreement(record.Agreement); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, "", err
	}
	record.AgreementID = firstNonEmptyStore(strings.TrimSpace(record.AgreementID), record.Agreement.ID)
	record.Version = firstPositive(record.Version, record.Agreement.Version)
	record.AgentID = firstNonEmptyStore(strings.TrimSpace(record.AgentID), record.Agreement.AgentID)
	record.AuthorityPrincipal = firstNonEmptyStore(strings.TrimSpace(record.AuthorityPrincipal), record.Agreement.Principals.AuthorityPrincipal)
	record.ReviewPrincipal = firstNonEmptyStore(strings.TrimSpace(record.ReviewPrincipal), record.Agreement.Principals.ReviewPrincipal)
	record.RuntimeKind = firstNonEmptyStore(normalizeEnumValue(record.RuntimeKind), record.Agreement.RuntimeKind)
	if record.AgreementID == "" || record.Version <= 0 || record.AgentID == "" {
		return DurableChildWorkAgreementVersionRecord{}, "", fmt.Errorf("durable child work agreement version requires agreement_id, version, and agent_id")
	}
	hash, err := core.StableExternalRuntimeContractHash(record.Agreement)
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, "", err
	}
	record.AgreementHash = firstNonEmptyStore(strings.TrimSpace(record.AgreementHash), hash)
	if record.AgreementHash != hash {
		return DurableChildWorkAgreementVersionRecord{}, "", fmt.Errorf("durable child work agreement hash mismatch")
	}
	record.Status = NormalizeDurableChildWorkAgreementVersionStatus(record.Status)
	if record.Status == "" {
		record.Status = DurableChildWorkAgreementVersionStatusProposed
	}
	record.SourceRequestID = strings.TrimSpace(record.SourceRequestID)
	now := time.Now().UTC()
	record.CreatedAt = nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	record.ActivatedAt = normalizeOptionalTime(record.ActivatedAt)
	record.SupersededAt = normalizeOptionalTime(record.SupersededAt)
	record.RevokedAt = normalizeOptionalTime(record.RevokedAt)
	agreementJSON, err := marshalExternalRuntimeJSON(record.Agreement)
	return record, agreementJSON, err
}

func normalizeDurableChildConditionalGrantRecord(record DurableChildConditionalGrantRecord) (DurableChildConditionalGrantRecord, string, string, string, error) {
	record.Grant = core.NormalizeConditionalGrant(record.Grant)
	if err := core.ValidateConditionalGrant(record.Grant); err != nil {
		return DurableChildConditionalGrantRecord{}, "", "", "", err
	}
	record.AgreementID = firstNonEmptyStore(strings.TrimSpace(record.AgreementID), record.Grant.WorkAgreementID)
	record.AgreementVersion = firstPositive(record.AgreementVersion, record.Grant.WorkAgreementVersion)
	record.GrantID = firstNonEmptyStore(strings.TrimSpace(record.GrantID), record.Grant.ID)
	record.AgentID = strings.TrimSpace(record.AgentID)
	if record.AgentID == "" {
		return DurableChildConditionalGrantRecord{}, "", "", "", fmt.Errorf("durable child conditional grant requires agent_id")
	}
	record.Status = firstNonEmptyStore(normalizeEnumValue(record.Status), firstNonEmptyStore(record.Grant.Status, "active"))
	record.Capability = record.Grant.Capability
	record.Tool = record.Grant.Tool
	record.Actions = append([]string(nil), record.Grant.Actions...)
	record.Triggers = append([]string(nil), record.Grant.Conditions.Triggers...)
	record.CredentialScope = record.Grant.CredentialScope
	now := time.Now().UTC()
	record.CreatedAt = nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	grantJSON, err := marshalExternalRuntimeJSON(record.Grant)
	if err != nil {
		return DurableChildConditionalGrantRecord{}, "", "", "", err
	}
	actionsJSON, err := marshalExternalRuntimeJSON(record.Actions)
	if err != nil {
		return DurableChildConditionalGrantRecord{}, "", "", "", err
	}
	triggersJSON, err := marshalExternalRuntimeJSON(record.Triggers)
	if err != nil {
		return DurableChildConditionalGrantRecord{}, "", "", "", err
	}
	return record, grantJSON, actionsJSON, triggersJSON, nil
}

func normalizeDurableChildWorkAgreementAmendmentRecord(record DurableChildWorkAgreementAmendmentRecord) (DurableChildWorkAgreementAmendmentRecord, string, string, error) {
	record.Amendment = core.NormalizeWorkAgreementAmendment(record.Amendment)
	if err := core.ValidateWorkAgreementAmendment(record.Amendment); err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, "", "", err
	}
	record.AmendmentID = firstNonEmptyStore(strings.TrimSpace(record.AmendmentID), record.Amendment.ID)
	record.AgreementID = firstNonEmptyStore(strings.TrimSpace(record.AgreementID), record.Amendment.WorkAgreementID)
	record.FromVersion = firstPositive(record.FromVersion, record.Amendment.FromVersion)
	record.ProposedVersion = firstPositive(record.ProposedVersion, record.Amendment.ProposedVersion)
	record.ProposedBy = firstNonEmptyStore(strings.TrimSpace(record.ProposedBy), record.Amendment.ProposedBy)
	record.Status = NormalizeDurableChildWorkAgreementAmendmentStatus(record.Status)
	if record.Status == "" {
		record.Status = DurableChildWorkAgreementAmendmentStatusProposed
	}
	record.ChangeClass = append([]string(nil), record.Amendment.ChangeClass...)
	record.SourceRequestID = strings.TrimSpace(record.SourceRequestID)
	now := time.Now().UTC()
	record.CreatedAt = nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	record.UpdatedAt = nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	record.ResolvedAt = normalizeOptionalTime(record.ResolvedAt)
	amendmentJSON, err := marshalExternalRuntimeJSON(record.Amendment)
	if err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, "", "", err
	}
	changeClassJSON, err := marshalExternalRuntimeJSON(record.ChangeClass)
	return record, amendmentJSON, changeClassJSON, err
}

func NormalizeDurableChildRuntimeSpecStatus(status DurableChildRuntimeSpecStatus) DurableChildRuntimeSpecStatus {
	switch DurableChildRuntimeSpecStatus(normalizeEnumValue(string(status))) {
	case DurableChildRuntimeSpecStatusPending, DurableChildRuntimeSpecStatusVerified, DurableChildRuntimeSpecStatusStale, DurableChildRuntimeSpecStatusRevoked, DurableChildRuntimeSpecStatusFailed:
		return DurableChildRuntimeSpecStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func NormalizeDurableChildWorkAgreementVersionStatus(status DurableChildWorkAgreementVersionStatus) DurableChildWorkAgreementVersionStatus {
	switch DurableChildWorkAgreementVersionStatus(normalizeEnumValue(string(status))) {
	case DurableChildWorkAgreementVersionStatusProposed, DurableChildWorkAgreementVersionStatusActive, DurableChildWorkAgreementVersionStatusRejected, DurableChildWorkAgreementVersionStatusSuperseded, DurableChildWorkAgreementVersionStatusRevoked:
		return DurableChildWorkAgreementVersionStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func NormalizeDurableChildWorkAgreementAmendmentStatus(status DurableChildWorkAgreementAmendmentStatus) DurableChildWorkAgreementAmendmentStatus {
	switch DurableChildWorkAgreementAmendmentStatus(normalizeEnumValue(string(status))) {
	case DurableChildWorkAgreementAmendmentStatusProposed, DurableChildWorkAgreementAmendmentStatusApproved, DurableChildWorkAgreementAmendmentStatusRejected, DurableChildWorkAgreementAmendmentStatusSuperseded:
		return DurableChildWorkAgreementAmendmentStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func ensureDurableChildRuntimeSpecContentUnchanged(db durableExternalRuntimeSQL, specID string, specHash string, specJSON string) error {
	var existingHash, existingJSON string
	err := db.QueryRow(`SELECT spec_hash, spec_json FROM durable_child_runtime_specs WHERE spec_id = ?`, specID).Scan(&existingHash, &existingJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load durable child runtime spec content: %w", err)
	}
	if existingHash != specHash || existingJSON != specJSON {
		return fmt.Errorf("durable child runtime spec %q content is immutable", specID)
	}
	return nil
}

func ensureDurableChildWorkAgreementContentUnchanged(db durableExternalRuntimeSQL, agreementID string, version int, agreementHash string, agreementJSON string) error {
	var existingHash, existingJSON string
	err := db.QueryRow(`
		SELECT agreement_hash, agreement_json
		FROM durable_child_work_agreement_versions
		WHERE agreement_id = ? AND version = ?
	`, agreementID, version).Scan(&existingHash, &existingJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load durable child work agreement content: %w", err)
	}
	if existingHash != agreementHash || existingJSON != agreementJSON {
		return fmt.Errorf("durable child work agreement %s/%d content is immutable", agreementID, version)
	}
	return nil
}

func ensureDurableChildConditionalGrantContentUnchanged(db durableExternalRuntimeSQL, agreementID string, version int, grantID string, grantJSON string) error {
	var existingJSON string
	err := db.QueryRow(`
		SELECT grant_json
		FROM durable_child_conditional_grants
		WHERE agreement_id = ? AND agreement_version = ? AND grant_id = ?
	`, agreementID, version, grantID).Scan(&existingJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load durable child conditional grant content: %w", err)
	}
	if existingJSON != grantJSON {
		return fmt.Errorf("durable child conditional grant %s/%d/%s content is immutable", agreementID, version, grantID)
	}
	return nil
}

func validateDraftGrantSet(agreement DurableChildWorkAgreementVersionRecord, grants []DurableChildConditionalGrantRecord) error {
	required := make(map[string]struct{}, len(agreement.Agreement.ConditionalGrantIDs))
	for _, grantID := range agreement.Agreement.ConditionalGrantIDs {
		required[grantID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(grants))
	for _, grant := range grants {
		if grant.AgentID != agreement.AgentID || grant.AgreementID != agreement.AgreementID || grant.AgreementVersion != agreement.Version {
			return fmt.Errorf("conditional grant %q does not match work agreement %s/%d", grant.GrantID, agreement.AgreementID, agreement.Version)
		}
		if _, ok := required[grant.GrantID]; !ok {
			return fmt.Errorf("conditional grant %q is not referenced by work agreement %s/%d", grant.GrantID, agreement.AgreementID, agreement.Version)
		}
		if _, ok := seen[grant.GrantID]; ok {
			return fmt.Errorf("conditional grant %q is duplicated in work agreement draft", grant.GrantID)
		}
		seen[grant.GrantID] = struct{}{}
	}
	for grantID := range required {
		if _, ok := seen[grantID]; !ok {
			return fmt.Errorf("work agreement %s/%d missing conditional grant %q", agreement.AgreementID, agreement.Version, grantID)
		}
	}
	return nil
}

func validateRuntimeSpecReadyForWorkAgreement(spec DurableChildRuntimeSpecRecord, agreement DurableChildWorkAgreementVersionRecord) error {
	if spec.AgentID != agreement.AgentID {
		return fmt.Errorf("runtime spec agent %q does not match work agreement agent %q", spec.AgentID, agreement.AgentID)
	}
	if spec.RuntimeKind != agreement.RuntimeKind {
		return fmt.Errorf("runtime spec kind %q does not match work agreement runtime_kind %q", spec.RuntimeKind, agreement.RuntimeKind)
	}
	if spec.InstallStatus != DurableChildRuntimeSpecStatusVerified {
		return fmt.Errorf("runtime spec %q is not verified", spec.SpecID)
	}
	if strings.TrimSpace(spec.ProbeStatus) != "passed" {
		return fmt.Errorf("runtime spec %q probe_status is not passed", spec.SpecID)
	}
	switch strings.TrimSpace(spec.DriftStatus) {
	case "", "clean", "fresh":
		return nil
	default:
		return fmt.Errorf("runtime spec %q drift_status %q is not clean", spec.SpecID, spec.DriftStatus)
	}
}

func validateLeaseMaterializationFenceForInsertTx(tx *sql.Tx, materialization core.LeaseMaterialization, lease core.MaterializedLease) (DurableChildWorkAgreementVersionRecord, DurableChildConditionalGrantRecord, error) {
	if lease.ConditionalGrantAgreementVersion != materialization.WorkAgreementVersion {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization grant version fence mismatch: agreement_version=%d conditional_grant_agreement_version=%d", materialization.WorkAgreementVersion, lease.ConditionalGrantAgreementVersion)
	}
	agreement, err := durableChildWorkAgreementVersionSQL(tx, materialization.WorkAgreementID, materialization.WorkAgreementVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization references missing work agreement %s/%d", materialization.WorkAgreementID, materialization.WorkAgreementVersion)
	}
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, err
	}
	if agreement.AgentID != materialization.AgentID {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization agent %q does not match work agreement agent %q", materialization.AgentID, agreement.AgentID)
	}
	grant, err := durableChildConditionalGrantSQL(tx, materialization.WorkAgreementID, lease.ConditionalGrantAgreementVersion, lease.ConditionalGrantID)
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization references missing conditional grant %s/%d/%s", materialization.WorkAgreementID, lease.ConditionalGrantAgreementVersion, lease.ConditionalGrantID)
	}
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, err
	}
	if grant.AgentID != materialization.AgentID {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization agent %q does not match conditional grant agent %q", materialization.AgentID, grant.AgentID)
	}
	if grant.Capability != lease.Capability {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization capability %q does not match conditional grant capability %q", lease.Capability, grant.Capability)
	}
	if grant.Grant.Materializes.LeaseKind != lease.LeaseKind {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization kind %q does not match conditional grant lease kind %q", lease.LeaseKind, grant.Grant.Materializes.LeaseKind)
	}
	if grant.Grant.Materializes.ReviewRoute != lease.ReviewRoute {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization review_route %q does not match conditional grant review_route %q", lease.ReviewRoute, grant.Grant.Materializes.ReviewRoute)
	}
	if grant.Grant.Materializes.SingleUse != lease.SingleUse {
		return DurableChildWorkAgreementVersionRecord{}, DurableChildConditionalGrantRecord{}, fmt.Errorf("lease materialization single_use=%t does not match conditional grant single_use=%t", lease.SingleUse, grant.Grant.Materializes.SingleUse)
	}
	return agreement, grant, nil
}

func updateWorkAgreementAmendmentStatusForRequestTx(tx *sql.Tx, requestID string, next DurableChildWorkAgreementAmendmentStatus, now time.Time) error {
	switch next {
	case DurableChildWorkAgreementAmendmentStatusProposed:
		return nil
	case DurableChildWorkAgreementAmendmentStatusRejected:
		if _, err := tx.Exec(`
			UPDATE durable_child_work_agreement_amendments
			SET status = 'rejected', updated_at = ?, resolved_at = ?
			WHERE source_request_id = ? AND status = 'proposed'
		`, formatSQLiteTime(now), formatSQLiteTime(now), requestID); err != nil {
			return fmt.Errorf("reject proposed work agreement amendment: %w", err)
		}
		return nil
	case DurableChildWorkAgreementAmendmentStatusApproved:
	default:
		return nil
	}
	rows, err := tx.Query(`
		SELECT amendment_id, agreement_id, from_version, proposed_version
		FROM durable_child_work_agreement_amendments
		WHERE source_request_id = ? AND status = 'proposed'
	`, requestID)
	if err != nil {
		return fmt.Errorf("query proposed work agreement amendments for request: %w", err)
	}
	var amendments []DurableChildWorkAgreementAmendmentRecord
	for rows.Next() {
		var amendment DurableChildWorkAgreementAmendmentRecord
		if err := rows.Scan(&amendment.AmendmentID, &amendment.AgreementID, &amendment.FromVersion, &amendment.ProposedVersion); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan proposed work agreement amendment: %w", err)
		}
		amendments = append(amendments, amendment)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close proposed work agreement amendment rows: %w", err)
	}
	for _, amendment := range amendments {
		if err := validateAmendmentBaseActiveTx(tx, amendment.AgreementID, amendment.FromVersion); err != nil {
			return err
		}
		if err := activateWorkAgreementVersionTx(tx, amendment.AgreementID, amendment.ProposedVersion, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE durable_child_work_agreement_amendments
			SET status = 'approved', updated_at = ?, resolved_at = ?
			WHERE amendment_id = ? AND status = 'proposed'
		`, formatSQLiteTime(now), formatSQLiteTime(now), amendment.AmendmentID); err != nil {
			return fmt.Errorf("approve work agreement amendment: %w", err)
		}
	}
	return nil
}

func validateAmendmentBaseActiveTx(tx *sql.Tx, agreementID string, fromVersion int) error {
	var statusRaw string
	err := tx.QueryRow(`
		SELECT status
		FROM durable_child_work_agreement_versions
		WHERE agreement_id = ? AND version = ?
	`, agreementID, fromVersion).Scan(&statusRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("work agreement amendment base %s/%d not found", agreementID, fromVersion)
	}
	if err != nil {
		return fmt.Errorf("load work agreement amendment base: %w", err)
	}
	if DurableChildWorkAgreementVersionStatus(statusRaw) != DurableChildWorkAgreementVersionStatusActive {
		return fmt.Errorf("work agreement amendment base %s/%d is not active", agreementID, fromVersion)
	}
	return nil
}

func updateWorkAgreementStatusForRequestTx(tx *sql.Tx, requestID string, next DurableChildWorkAgreementVersionStatus, now time.Time) error {
	switch next {
	case DurableChildWorkAgreementVersionStatusProposed:
		return nil
	case DurableChildWorkAgreementVersionStatusRejected:
		if _, err := tx.Exec(`
			UPDATE durable_child_work_agreement_versions
			SET status = 'rejected', updated_at = ?
			WHERE source_request_id = ? AND status = 'proposed'
		`, formatSQLiteTime(now), requestID); err != nil {
			return fmt.Errorf("reject proposed work agreement version: %w", err)
		}
		return nil
	case DurableChildWorkAgreementVersionStatusActive:
	default:
		return nil
	}
	rows, err := tx.Query(`
		SELECT agreement_id, version
		FROM durable_child_work_agreement_versions
		WHERE source_request_id = ? AND status IN ('proposed', 'active')
	`, requestID)
	if err != nil {
		return fmt.Errorf("query approvable work agreements: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var agreementID string
		var version int
		if err := rows.Scan(&agreementID, &version); err != nil {
			return fmt.Errorf("scan activated work agreement: %w", err)
		}
		if err := activateWorkAgreementVersionTx(tx, agreementID, version, now); err != nil {
			return err
		}
	}
	return rows.Err()
}

func activateWorkAgreementVersionTx(tx *sql.Tx, agreementID string, version int, now time.Time) error {
	if agreementID == "" || version <= 0 {
		return nil
	}
	var agentID string
	var statusRaw string
	err := tx.QueryRow(`
		SELECT agent_id, status
		FROM durable_child_work_agreement_versions
		WHERE agreement_id = ? AND version = ?
	`, agreementID, version).Scan(&agentID, &statusRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("activate work agreement version %s/%d: not found", agreementID, version)
	}
	if err != nil {
		return fmt.Errorf("load work agreement version for activation: %w", err)
	}
	status := DurableChildWorkAgreementVersionStatus(statusRaw)
	if status == DurableChildWorkAgreementVersionStatusActive {
		return nil
	}
	if status != DurableChildWorkAgreementVersionStatusProposed {
		return fmt.Errorf("activate work agreement version %s/%d: status %q is not proposed", agreementID, version, status)
	}
	if _, err := tx.Exec(`
		UPDATE durable_child_work_agreement_versions
		SET status = 'superseded', updated_at = ?, superseded_at = ?
		WHERE agent_id = ? AND agreement_id = ? AND version != ? AND status = 'active'
	`, formatSQLiteTime(now), formatSQLiteTime(now), agentID, agreementID, version); err != nil {
		return fmt.Errorf("supersede previous work agreement version: %w", err)
	}
	result, err := tx.Exec(`
		UPDATE durable_child_work_agreement_versions
		SET status = 'active', updated_at = ?, activated_at = COALESCE(activated_at, ?)
		WHERE agreement_id = ? AND version = ? AND status = 'proposed'
	`, formatSQLiteTime(now), formatSQLiteTime(now), agreementID, version)
	if err != nil {
		return fmt.Errorf("activate work agreement version: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect work agreement activation: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("activate work agreement version %s/%d: no proposed row changed", agreementID, version)
	}
	return nil
}

func revokeDurableChildWorkAgreementVersionTx(tx *sql.Tx, agentID string, agreementID string, version int, at time.Time) (DurableChildWorkAgreementVersionRecord, bool, int64, error) {
	var currentStatusRaw string
	err := tx.QueryRow(`
		SELECT status
		FROM durable_child_work_agreement_versions
		WHERE agent_id = ? AND agreement_id = ? AND version = ?
	`, agentID, agreementID, version).Scan(&currentStatusRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, sql.ErrNoRows
	}
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, fmt.Errorf("load work agreement version for revocation: %w", err)
	}
	changed := DurableChildWorkAgreementVersionStatus(currentStatusRaw) != DurableChildWorkAgreementVersionStatusRevoked
	if changed {
		result, err := tx.Exec(`
			UPDATE durable_child_work_agreement_versions
			SET status = 'revoked', updated_at = ?, revoked_at = COALESCE(revoked_at, ?)
			WHERE agent_id = ? AND agreement_id = ? AND version = ? AND status != 'revoked'
		`, formatSQLiteTime(at), formatSQLiteTime(at), agentID, agreementID, version)
		if err != nil {
			return DurableChildWorkAgreementVersionRecord{}, false, 0, fmt.Errorf("revoke work agreement version: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return DurableChildWorkAgreementVersionRecord{}, false, 0, fmt.Errorf("inspect work agreement revocation: %w", err)
		}
		changed = affected == 1
	}
	leaseResult, err := tx.Exec(`
		UPDATE durable_child_lease_materializations
		SET status = 'revoked', revoked_at = COALESCE(revoked_at, ?)
		WHERE agent_id = ? AND agreement_id = ? AND agreement_version = ? AND status = 'active'
	`, formatSQLiteTime(at), agentID, agreementID, version)
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, fmt.Errorf("revoke work agreement active leases: %w", err)
	}
	revokedLeases, err := leaseResult.RowsAffected()
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, fmt.Errorf("inspect revoked work agreement leases: %w", err)
	}
	record, err := durableChildWorkAgreementVersionSQL(tx, agreementID, version)
	if err != nil {
		return DurableChildWorkAgreementVersionRecord{}, false, 0, err
	}
	return record, changed, revokedLeases, nil
}

func (s *SQLiteStore) transitionDurableChildLease(leaseID string, next DurableChildLeaseMaterializationStatus, at time.Time) (DurableChildLeaseMaterializationRecord, bool, error) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return DurableChildLeaseMaterializationRecord{}, false, nil
	}
	at = nonZeroTimeOrNow(at, time.Now().UTC()).UTC()
	column := "consumed_at"
	if next == DurableChildLeaseMaterializationStatusRevoked {
		column = "revoked_at"
	}
	result, err := s.db.Exec(`
		UPDATE durable_child_lease_materializations
		SET status = ?, `+column+` = ?
		WHERE lease_id = ? AND status = 'active'
	`, string(next), formatSQLiteTime(at), leaseID)
	if err != nil {
		return DurableChildLeaseMaterializationRecord{}, false, fmt.Errorf("transition durable child lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return DurableChildLeaseMaterializationRecord{}, false, fmt.Errorf("inspect durable child lease transition: %w", err)
	}
	record, ok, err := s.DurableChildLeaseMaterialization(leaseID)
	if err != nil {
		return DurableChildLeaseMaterializationRecord{}, false, err
	}
	return record, affected == 1 && ok, nil
}

func durableChildRuntimeSpecSelect() string {
	return `SELECT spec_id, agent_id, spec_hash, runtime_kind, runtime_mode, source_ref, spec_json,
		install_status, probe_status, drift_status, source_request_id, source_review_event_id,
		created_at, updated_at, verified_at, stale_at
		FROM durable_child_runtime_specs`
}

func durableChildWorkAgreementVersionSelect() string {
	return `SELECT agreement_id, version, agent_id, status, authority_principal, review_principal,
		runtime_kind, agreement_hash, agreement_json, source_request_id, source_review_event_id,
		created_at, updated_at, activated_at, superseded_at, revoked_at
		FROM durable_child_work_agreement_versions`
}

func durableChildConditionalGrantSelect() string {
	return `SELECT agreement_id, agreement_version, grant_id, agent_id, status, capability, tool,
		actions_json, triggers_json, credential_scope, grant_json, created_at, updated_at
		FROM durable_child_conditional_grants`
}

func durableChildLeaseMaterializationSelect() string {
	return `SELECT lease_id, materialization_id, agent_id, agreement_id, agreement_version,
		conditional_grant_id, conditional_grant_agreement_version, capability, lease_kind, review_route,
		runtime_spec_hash, trigger, single_use, status, materialization_json,
		created_at, expires_at, consumed_at, revoked_at
		FROM durable_child_lease_materializations`
}

func durableChildWorkAgreementAmendmentSelect() string {
	return `SELECT amendment_id, agreement_id, from_version, proposed_version, proposed_by,
		status, change_class_json, amendment_json, source_request_id, source_review_event_id,
		result_review_event_id, created_at, updated_at, resolved_at
		FROM durable_child_work_agreement_amendments`
}

type durableExternalRuntimeScanner interface {
	Scan(dest ...any) error
}

func scanDurableChildRuntimeSpec(scanner durableExternalRuntimeScanner) (DurableChildRuntimeSpecRecord, error) {
	var record DurableChildRuntimeSpecRecord
	var specJSON, statusRaw, createdRaw, updatedRaw string
	var verifiedRaw, staleRaw sql.NullString
	if err := scanner.Scan(&record.SpecID, &record.AgentID, &record.SpecHash, &record.RuntimeKind, &record.RuntimeMode, &record.SourceRef, &specJSON,
		&statusRaw, &record.ProbeStatus, &record.DriftStatus, &record.SourceRequestID, &record.SourceReviewEventID,
		&createdRaw, &updatedRaw, &verifiedRaw, &staleRaw); err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	if err := json.Unmarshal([]byte(specJSON), &record.Spec); err != nil {
		return DurableChildRuntimeSpecRecord{}, fmt.Errorf("unmarshal durable child runtime spec: %w", err)
	}
	record.InstallStatus = DurableChildRuntimeSpecStatus(statusRaw)
	var err error
	if record.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	if record.UpdatedAt, err = parseSQLiteTime(updatedRaw); err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	if record.VerifiedAt, err = parseNullableSQLiteTime(verifiedRaw); err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	if record.StaleAt, err = parseNullableSQLiteTime(staleRaw); err != nil {
		return DurableChildRuntimeSpecRecord{}, err
	}
	return record, nil
}

func scanDurableChildWorkAgreementVersion(scanner durableExternalRuntimeScanner) (DurableChildWorkAgreementVersionRecord, error) {
	var record DurableChildWorkAgreementVersionRecord
	var statusRaw, agreementJSON, createdRaw, updatedRaw string
	var activatedRaw, supersededRaw, revokedRaw sql.NullString
	if err := scanner.Scan(&record.AgreementID, &record.Version, &record.AgentID, &statusRaw, &record.AuthorityPrincipal, &record.ReviewPrincipal,
		&record.RuntimeKind, &record.AgreementHash, &agreementJSON, &record.SourceRequestID, &record.SourceReviewEventID,
		&createdRaw, &updatedRaw, &activatedRaw, &supersededRaw, &revokedRaw); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	if err := json.Unmarshal([]byte(agreementJSON), &record.Agreement); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, fmt.Errorf("unmarshal durable child work agreement: %w", err)
	}
	record.Status = DurableChildWorkAgreementVersionStatus(statusRaw)
	var err error
	if record.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	if record.UpdatedAt, err = parseSQLiteTime(updatedRaw); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	if record.ActivatedAt, err = parseNullableSQLiteTime(activatedRaw); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	if record.SupersededAt, err = parseNullableSQLiteTime(supersededRaw); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	if record.RevokedAt, err = parseNullableSQLiteTime(revokedRaw); err != nil {
		return DurableChildWorkAgreementVersionRecord{}, err
	}
	return record, nil
}

func scanDurableChildConditionalGrant(scanner durableExternalRuntimeScanner) (DurableChildConditionalGrantRecord, error) {
	var record DurableChildConditionalGrantRecord
	var actionsJSON, triggersJSON, grantJSON, createdRaw, updatedRaw string
	if err := scanner.Scan(&record.AgreementID, &record.AgreementVersion, &record.GrantID, &record.AgentID, &record.Status, &record.Capability, &record.Tool,
		&actionsJSON, &triggersJSON, &record.CredentialScope, &grantJSON, &createdRaw, &updatedRaw); err != nil {
		return DurableChildConditionalGrantRecord{}, err
	}
	_ = json.Unmarshal([]byte(actionsJSON), &record.Actions)
	_ = json.Unmarshal([]byte(triggersJSON), &record.Triggers)
	if err := json.Unmarshal([]byte(grantJSON), &record.Grant); err != nil {
		return DurableChildConditionalGrantRecord{}, fmt.Errorf("unmarshal durable child conditional grant: %w", err)
	}
	var err error
	if record.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return DurableChildConditionalGrantRecord{}, err
	}
	if record.UpdatedAt, err = parseSQLiteTime(updatedRaw); err != nil {
		return DurableChildConditionalGrantRecord{}, err
	}
	return record, nil
}

func scanDurableChildConditionalGrantRows(rows *sql.Rows) ([]DurableChildConditionalGrantRecord, error) {
	var out []DurableChildConditionalGrantRecord
	for rows.Next() {
		record, err := scanDurableChildConditionalGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable child conditional grants: %w", err)
	}
	return out, nil
}

func scanDurableChildLeaseMaterialization(scanner durableExternalRuntimeScanner) (DurableChildLeaseMaterializationRecord, error) {
	var record DurableChildLeaseMaterializationRecord
	var statusRaw, materializationJSON, createdRaw string
	var singleUse int
	var expiresRaw, consumedRaw, revokedRaw sql.NullString
	if err := scanner.Scan(&record.LeaseID, &record.MaterializationID, &record.AgentID, &record.AgreementID, &record.AgreementVersion,
		&record.ConditionalGrantID, &record.ConditionalGrantAgreementVersion, &record.Capability, &record.LeaseKind, &record.ReviewRoute,
		&record.RuntimeSpecHash, &record.Trigger, &singleUse, &statusRaw, &materializationJSON,
		&createdRaw, &expiresRaw, &consumedRaw, &revokedRaw); err != nil {
		return DurableChildLeaseMaterializationRecord{}, err
	}
	record.SingleUse = singleUse != 0
	record.Status = DurableChildLeaseMaterializationStatus(statusRaw)
	if err := json.Unmarshal([]byte(materializationJSON), &record.Materialization); err != nil {
		return DurableChildLeaseMaterializationRecord{}, fmt.Errorf("unmarshal durable child lease materialization: %w", err)
	}
	var err error
	if record.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return DurableChildLeaseMaterializationRecord{}, err
	}
	if record.ExpiresAt, err = parseNullableSQLiteTime(expiresRaw); err != nil {
		return DurableChildLeaseMaterializationRecord{}, err
	}
	if record.ConsumedAt, err = parseNullableSQLiteTime(consumedRaw); err != nil {
		return DurableChildLeaseMaterializationRecord{}, err
	}
	if record.RevokedAt, err = parseNullableSQLiteTime(revokedRaw); err != nil {
		return DurableChildLeaseMaterializationRecord{}, err
	}
	return record, nil
}

func scanDurableChildWorkAgreementAmendment(scanner durableExternalRuntimeScanner) (DurableChildWorkAgreementAmendmentRecord, error) {
	var record DurableChildWorkAgreementAmendmentRecord
	var statusRaw, changeClassJSON, amendmentJSON, createdRaw, updatedRaw string
	var resolvedRaw sql.NullString
	if err := scanner.Scan(&record.AmendmentID, &record.AgreementID, &record.FromVersion, &record.ProposedVersion, &record.ProposedBy,
		&statusRaw, &changeClassJSON, &amendmentJSON, &record.SourceRequestID, &record.SourceReviewEventID,
		&record.ResultReviewEventID, &createdRaw, &updatedRaw, &resolvedRaw); err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, err
	}
	record.Status = DurableChildWorkAgreementAmendmentStatus(statusRaw)
	_ = json.Unmarshal([]byte(changeClassJSON), &record.ChangeClass)
	if err := json.Unmarshal([]byte(amendmentJSON), &record.Amendment); err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, fmt.Errorf("unmarshal durable child work agreement amendment: %w", err)
	}
	var err error
	if record.CreatedAt, err = parseSQLiteTime(createdRaw); err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, err
	}
	if record.UpdatedAt, err = parseSQLiteTime(updatedRaw); err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, err
	}
	if record.ResolvedAt, err = parseNullableSQLiteTime(resolvedRaw); err != nil {
		return DurableChildWorkAgreementAmendmentRecord{}, err
	}
	return record, nil
}

func workAgreementForStorage(agreement core.WorkAgreement) core.WorkAgreement {
	agreement = core.NormalizeWorkAgreement(agreement)
	agreement.Status = ""
	return agreement
}

func runtimeSourceRef(ref core.RuntimeSourceRef) string {
	ref = core.NormalizeRuntimeSourceRef(ref)
	switch ref.Kind {
	case "git":
		return strings.TrimSpace(ref.Repo + "@" + ref.Ref)
	case "binary", "container_image":
		return firstNonEmptyStore(ref.Ref, ref.Digest)
	default:
		return firstNonEmptyStore(ref.Ref, ref.Digest, ref.Repo)
	}
}

func marshalExternalRuntimeJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func externalRuntimeStoreID(prefix string, parts ...string) string {
	clean := make([]string, 0, len(parts)+1)
	clean = append(clean, strings.TrimSpace(prefix))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ReplaceAll(part, "sha256:", ""))
		if part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, ":")
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatSQLiteTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableSQLiteTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatSQLiteTime(value)
}

func normalizeOptionalTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func parseNullableSQLiteTime(value sql.NullString) (time.Time, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return time.Time{}, nil
	}
	return parseSQLiteTime(value.String)
}
