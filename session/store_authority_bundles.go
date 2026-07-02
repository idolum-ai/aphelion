//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertAuthorityBundleContract(input AuthorityBundleContract) (AuthorityBundleContract, error) {
	if s == nil || s.db == nil {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract store unavailable")
	}
	input, err := CanonicalizeAuthorityBundleContract(input)
	if err != nil {
		return AuthorityBundleContract{}, err
	}
	if input.BundleID == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract bundle_id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AuthorityBundleContract{}, fmt.Errorf("begin authority bundle contract tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	contract, err := upsertAuthorityBundleContractTx(tx, input)
	if err != nil {
		return AuthorityBundleContract{}, err
	}
	if err := tx.Commit(); err != nil {
		return AuthorityBundleContract{}, fmt.Errorf("commit authority bundle contract tx: %w", err)
	}
	return contract, nil
}

func (s *SQLiteStore) AuthorityBundleContract(bundleID string) (AuthorityBundleContract, bool, error) {
	if s == nil || s.db == nil {
		return AuthorityBundleContract{}, false, fmt.Errorf("authority bundle contract store unavailable")
	}
	return authorityBundleContractByID(s.db, bundleID)
}

func upsertAuthorityBundleContractTx(tx *sql.Tx, input AuthorityBundleContract) (AuthorityBundleContract, error) {
	input, err := CanonicalizeAuthorityBundleContract(input)
	if err != nil {
		return AuthorityBundleContract{}, err
	}
	if input.BundleID == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract bundle_id is required")
	}
	existing, ok, err := authorityBundleContractByID(tx, input.BundleID)
	if err != nil {
		return AuthorityBundleContract{}, err
	}
	if ok {
		if authorityBundleContractEquivalent(existing, input) {
			return existing, nil
		}
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract %s idempotency conflict", input.BundleID)
	}
	sourceRefs := encodeStringList(input.SourceNextActionRecordIDs)
	allowed := encodeStringList(input.AllowedActions)
	forbidden := encodeStringList(input.ForbiddenActions)
	stops := encodeStringList(input.StopConditions)
	grantsRaw, _ := json.Marshal(input.RequiredCapabilityGrants)
	componentsRaw, _ := json.Marshal(input.Components)
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := input.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	expiresAt := nullableTime(input.ExpiresAt)
	if _, err := tx.Exec(`
		INSERT INTO authority_bundle_contracts(
			bundle_id, contract_version, request_instance_id, contract_hash,
			session_id, status, principal, objective, summary,
			source_next_action_record_ids_json, allowed_actions_json,
			forbidden_actions_json, stop_conditions_json,
			primary_continuation_contract_id, required_capability_grants_json,
			components_json, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.BundleID, input.ContractVersion, input.RequestInstanceID, input.ContractHash,
		input.SessionID, string(input.Status), input.Principal, input.Objective, input.Summary,
		sourceRefs, allowed, forbidden, stops, input.PrimaryContinuationContractID,
		string(grantsRaw), string(componentsRaw), expiresAt,
		createdAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano)); err != nil {
		return AuthorityBundleContract{}, fmt.Errorf("insert authority bundle contract %s: %w", input.BundleID, err)
	}
	return input, nil
}

type authorityBundleContractScanner interface {
	QueryRow(query string, args ...any) *sql.Row
}

func authorityBundleContractByID(db authorityBundleContractScanner, bundleID string) (AuthorityBundleContract, bool, error) {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return AuthorityBundleContract{}, false, nil
	}
	row := db.QueryRow(`
		SELECT bundle_id, contract_version, request_instance_id, contract_hash,
			session_id, status, principal, objective, summary,
			source_next_action_record_ids_json, allowed_actions_json,
			forbidden_actions_json, stop_conditions_json,
			primary_continuation_contract_id, required_capability_grants_json,
			components_json, expires_at, created_at, updated_at
		FROM authority_bundle_contracts
		WHERE bundle_id = ?
	`, bundleID)
	return scanAuthorityBundleContract(row)
}

func scanAuthorityBundleContract(scanner interface{ Scan(dest ...any) error }) (AuthorityBundleContract, bool, error) {
	var (
		contract      AuthorityBundleContract
		statusRaw     string
		sourceRefsRaw string
		allowedRaw    string
		forbiddenRaw  string
		stopsRaw      string
		grantsRaw     string
		componentsRaw string
		expiresAtRaw  sql.NullString
		createdAtRaw  string
		updatedAtRaw  string
	)
	if err := scanner.Scan(
		&contract.BundleID, &contract.ContractVersion, &contract.RequestInstanceID, &contract.ContractHash,
		&contract.SessionID, &statusRaw, &contract.Principal, &contract.Objective, &contract.Summary,
		&sourceRefsRaw, &allowedRaw, &forbiddenRaw, &stopsRaw, &contract.PrimaryContinuationContractID,
		&grantsRaw, &componentsRaw, &expiresAtRaw, &createdAtRaw, &updatedAtRaw,
	); err != nil {
		if err == sql.ErrNoRows {
			return AuthorityBundleContract{}, false, nil
		}
		return AuthorityBundleContract{}, false, fmt.Errorf("scan authority bundle contract: %w", err)
	}
	contract.Status = NormalizeAuthorityBundleStatus(AuthorityBundleStatus(statusRaw))
	contract.SourceNextActionRecordIDs = decodeStringList(sourceRefsRaw)
	contract.AllowedActions = decodeStringList(allowedRaw)
	contract.ForbiddenActions = decodeStringList(forbiddenRaw)
	contract.StopConditions = decodeStringList(stopsRaw)
	if err := json.Unmarshal([]byte(firstNonEmptyStore(grantsRaw, "[]")), &contract.RequiredCapabilityGrants); err != nil {
		return AuthorityBundleContract{}, false, fmt.Errorf("decode authority bundle grant specs: %w", err)
	}
	if err := json.Unmarshal([]byte(firstNonEmptyStore(componentsRaw, "[]")), &contract.Components); err != nil {
		return AuthorityBundleContract{}, false, fmt.Errorf("decode authority bundle components: %w", err)
	}
	if expiresAtRaw.Valid && strings.TrimSpace(expiresAtRaw.String) != "" {
		expiresAt, err := parseSQLiteTime(expiresAtRaw.String)
		if err != nil {
			return AuthorityBundleContract{}, false, fmt.Errorf("parse authority bundle expires_at: %w", err)
		}
		contract.ExpiresAt = expiresAt
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return AuthorityBundleContract{}, false, fmt.Errorf("parse authority bundle created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return AuthorityBundleContract{}, false, fmt.Errorf("parse authority bundle updated_at: %w", err)
	}
	contract.CreatedAt = createdAt
	contract.UpdatedAt = updatedAt
	contract, err = CanonicalizeAuthorityBundleContract(contract)
	if err != nil {
		return AuthorityBundleContract{}, false, err
	}
	return contract, true, nil
}

func authorityBundleContractEquivalent(left AuthorityBundleContract, right AuthorityBundleContract) bool {
	left = NormalizeAuthorityBundleContract(left)
	right = NormalizeAuthorityBundleContract(right)
	return left.BundleID == right.BundleID &&
		left.ContractVersion == right.ContractVersion &&
		left.RequestInstanceID == right.RequestInstanceID &&
		left.ContractHash == right.ContractHash &&
		left.SessionID == right.SessionID &&
		left.Status == right.Status &&
		left.Principal == right.Principal &&
		left.Objective == right.Objective &&
		left.Summary == right.Summary &&
		stringListEqual(left.SourceNextActionRecordIDs, right.SourceNextActionRecordIDs) &&
		stringListEqual(left.AllowedActions, right.AllowedActions) &&
		stringListEqual(left.ForbiddenActions, right.ForbiddenActions) &&
		stringListEqual(left.StopConditions, right.StopConditions) &&
		left.PrimaryContinuationContractID == right.PrimaryContinuationContractID &&
		capabilityGrantSpecsEqual(left.RequiredCapabilityGrants, right.RequiredCapabilityGrants) &&
		authorityBundleComponentsEqual(left.Components, right.Components) &&
		left.ExpiresAt.Equal(right.ExpiresAt)
}

func capabilityGrantSpecsEqual(left []CapabilityGrantSpec, right []CapabilityGrantSpec) bool {
	left = NormalizeCapabilityGrantSpecs(left)
	right = NormalizeCapabilityGrantSpecs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].RequestID != right[i].RequestID ||
			left[i].GrantID != right[i].GrantID ||
			left[i].Kind != right[i].Kind ||
			left[i].TargetResource != right[i].TargetResource ||
			left[i].GrantedTo != right[i].GrantedTo ||
			!stringListEqual(left[i].AllowedActions, right[i].AllowedActions) ||
			left[i].Contract != right[i].Contract ||
			left[i].Constraints != right[i].Constraints ||
			!left[i].ExpiresAt.Equal(right[i].ExpiresAt) {
			return false
		}
	}
	return true
}

func authorityBundleComponentsEqual(left []AuthorityBundleComponent, right []AuthorityBundleComponent) bool {
	left = normalizeAuthorityBundleComponents(left)
	right = normalizeAuthorityBundleComponents(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
