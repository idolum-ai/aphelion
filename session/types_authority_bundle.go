//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	AuthorityBundleContractVersionV1 = "aphelion.authority_bundle.v1"
	AuthorityBundleContractVersion   = "aphelion.authority_bundle.v2"
)

type AuthorityBundleStatus string

const (
	AuthorityBundleStatusRecorded   AuthorityBundleStatus = "recorded"
	AuthorityBundleStatusSuperseded AuthorityBundleStatus = "superseded"
)

type AuthorityBundleComponent struct {
	Kind       string `json:"kind,omitempty"`
	RefID      string `json:"ref_id,omitempty"`
	Subject    string `json:"subject,omitempty"`
	SubjectRef string `json:"subject_ref,omitempty"`
}

const (
	AuthorityBundleComponentKindCapabilityRequest            = "capability_request"
	AuthorityBundleComponentKindContinuationRecoveryContract = "continuation_recovery_contract"
	AuthorityBundleComponentKindNextAction                   = "next_action"
	AuthorityBundleComponentKindChildAuthorityBundle         = "child_authority_bundle"
	AuthorityBundleComponentKindChildTaskResult              = "child_task_result"
)

type AuthorityBundleContract struct {
	BundleID                      string                     `json:"bundle_id"`
	ContractVersion               string                     `json:"contract_version"`
	RequestInstanceID             string                     `json:"request_instance_id"`
	ContractHash                  string                     `json:"contract_hash"`
	SessionID                     string                     `json:"session_id,omitempty"`
	Status                        AuthorityBundleStatus      `json:"status,omitempty"`
	Principal                     string                     `json:"principal,omitempty"`
	Objective                     string                     `json:"objective,omitempty"`
	Summary                       string                     `json:"summary,omitempty"`
	SourceNextActionRecordIDs     []string                   `json:"source_next_action_record_ids,omitempty"`
	AllowedActions                []string                   `json:"allowed_actions,omitempty"`
	ForbiddenActions              []string                   `json:"forbidden_actions,omitempty"`
	StopConditions                []string                   `json:"stop_conditions,omitempty"`
	PrimaryContinuationContractID string                     `json:"primary_continuation_contract_id,omitempty"`
	RequiredCapabilityGrants      []CapabilityGrantSpec      `json:"required_capability_grants,omitempty"`
	Components                    []AuthorityBundleComponent `json:"components,omitempty"`
	ExpiresAt                     time.Time                  `json:"expires_at,omitempty"`
	CreatedAt                     time.Time                  `json:"created_at,omitempty"`
	UpdatedAt                     time.Time                  `json:"updated_at,omitempty"`
}

type AuthorityBundleContractInput struct {
	RequestInstanceID             string
	SessionID                     string
	Principal                     string
	Objective                     string
	Summary                       string
	SourceNextActionRecordIDs     []string
	AllowedActions                []string
	ForbiddenActions              []string
	StopConditions                []string
	PrimaryContinuationContractID string
	RequiredCapabilityGrants      []CapabilityGrantSpec
	Components                    []AuthorityBundleComponent
	ExpiresAt                     time.Time
	CreatedAt                     time.Time
}

func CompileAuthorityBundleContract(input AuthorityBundleContractInput) (AuthorityBundleContract, error) {
	input = normalizeAuthorityBundleContractInput(input)
	if input.RequestInstanceID == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires request_instance_id")
	}
	if input.SessionID == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires session_id")
	}
	if input.Principal == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires principal")
	}
	if input.Summary == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires summary")
	}
	if len(input.AllowedActions) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires allowed_actions")
	}
	if len(input.ForbiddenActions) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires forbidden_actions")
	}
	if len(input.StopConditions) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires stop_conditions")
	}
	if input.PrimaryContinuationContractID == "" && len(input.RequiredCapabilityGrants) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires a continuation contract or capability grant component")
	}
	if err := validateAuthorityBundleComponents(input.Components); err != nil {
		return AuthorityBundleContract{}, err
	}
	for _, forbidden := range input.ForbiddenActions {
		for _, allowed := range input.AllowedActions {
			if forbidden == allowed {
				return AuthorityBundleContract{}, fmt.Errorf("authority bundle action %q cannot be both allowed and forbidden", allowed)
			}
		}
	}
	hash := authorityBundleContractHash(input)
	id := authorityBundleContractID(input.RequestInstanceID, hash)
	now := input.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return NormalizeAuthorityBundleContract(AuthorityBundleContract{
		BundleID:                      id,
		ContractVersion:               AuthorityBundleContractVersion,
		RequestInstanceID:             input.RequestInstanceID,
		ContractHash:                  hash,
		SessionID:                     input.SessionID,
		Status:                        AuthorityBundleStatusRecorded,
		Principal:                     input.Principal,
		Objective:                     input.Objective,
		Summary:                       input.Summary,
		SourceNextActionRecordIDs:     input.SourceNextActionRecordIDs,
		AllowedActions:                input.AllowedActions,
		ForbiddenActions:              input.ForbiddenActions,
		StopConditions:                input.StopConditions,
		PrimaryContinuationContractID: input.PrimaryContinuationContractID,
		RequiredCapabilityGrants:      input.RequiredCapabilityGrants,
		Components:                    input.Components,
		ExpiresAt:                     input.ExpiresAt,
		CreatedAt:                     now,
		UpdatedAt:                     now,
	}), nil
}

func CanonicalizeAuthorityBundleContract(contract AuthorityBundleContract) (AuthorityBundleContract, error) {
	contract = NormalizeAuthorityBundleContract(contract)
	version := contract.ContractVersion
	input := normalizeAuthorityBundleContractInput(AuthorityBundleContractInput{
		RequestInstanceID:             contract.RequestInstanceID,
		SessionID:                     contract.SessionID,
		Principal:                     contract.Principal,
		Objective:                     contract.Objective,
		Summary:                       contract.Summary,
		SourceNextActionRecordIDs:     append([]string(nil), contract.SourceNextActionRecordIDs...),
		AllowedActions:                append([]string(nil), contract.AllowedActions...),
		ForbiddenActions:              append([]string(nil), contract.ForbiddenActions...),
		StopConditions:                append([]string(nil), contract.StopConditions...),
		PrimaryContinuationContractID: contract.PrimaryContinuationContractID,
		RequiredCapabilityGrants:      append([]CapabilityGrantSpec(nil), contract.RequiredCapabilityGrants...),
		Components:                    append([]AuthorityBundleComponent(nil), contract.Components...),
		ExpiresAt:                     contract.ExpiresAt,
		CreatedAt:                     contract.CreatedAt,
	})
	if input.RequestInstanceID == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires request_instance_id")
	}
	if version != AuthorityBundleContractVersionV1 && input.SessionID == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires session_id")
	}
	if input.Principal == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires principal")
	}
	if input.Summary == "" {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires summary")
	}
	if len(input.AllowedActions) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires allowed_actions")
	}
	if len(input.ForbiddenActions) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires forbidden_actions")
	}
	if len(input.StopConditions) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires stop_conditions")
	}
	if input.PrimaryContinuationContractID == "" && len(input.RequiredCapabilityGrants) == 0 {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract requires a continuation contract or capability grant component")
	}
	if err := validateAuthorityBundleComponents(input.Components); err != nil {
		return AuthorityBundleContract{}, err
	}
	for _, forbidden := range input.ForbiddenActions {
		for _, allowed := range input.AllowedActions {
			if forbidden == allowed {
				return AuthorityBundleContract{}, fmt.Errorf("authority bundle action %q cannot be both allowed and forbidden", allowed)
			}
		}
	}
	hash := authorityBundleContractHashForVersion(input, version)
	if contract.ContractHash != "" && contract.ContractHash != hash {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract %s hash mismatch", contract.BundleID)
	}
	id := authorityBundleContractID(input.RequestInstanceID, hash)
	if contract.BundleID != "" && contract.BundleID != id {
		return AuthorityBundleContract{}, fmt.Errorf("authority bundle contract %s id mismatch", contract.BundleID)
	}
	contract.BundleID = id
	contract.ContractVersion = version
	contract.RequestInstanceID = input.RequestInstanceID
	contract.ContractHash = hash
	contract.SessionID = input.SessionID
	contract.Principal = input.Principal
	contract.Objective = input.Objective
	contract.Summary = input.Summary
	contract.SourceNextActionRecordIDs = append([]string(nil), input.SourceNextActionRecordIDs...)
	contract.AllowedActions = append([]string(nil), input.AllowedActions...)
	contract.ForbiddenActions = append([]string(nil), input.ForbiddenActions...)
	contract.StopConditions = append([]string(nil), input.StopConditions...)
	contract.PrimaryContinuationContractID = input.PrimaryContinuationContractID
	contract.RequiredCapabilityGrants = append([]CapabilityGrantSpec(nil), input.RequiredCapabilityGrants...)
	contract.Components = append([]AuthorityBundleComponent(nil), input.Components...)
	contract.ExpiresAt = input.ExpiresAt
	return NormalizeAuthorityBundleContract(contract), nil
}

func NormalizeAuthorityBundleContract(contract AuthorityBundleContract) AuthorityBundleContract {
	contract.BundleID = strings.TrimSpace(contract.BundleID)
	contract.ContractVersion = strings.TrimSpace(contract.ContractVersion)
	if contract.ContractVersion == "" {
		contract.ContractVersion = AuthorityBundleContractVersion
	}
	contract.RequestInstanceID = strings.TrimSpace(contract.RequestInstanceID)
	contract.ContractHash = strings.TrimSpace(contract.ContractHash)
	contract.SessionID = strings.TrimSpace(contract.SessionID)
	contract.Status = NormalizeAuthorityBundleStatus(contract.Status)
	contract.Principal = strings.TrimSpace(contract.Principal)
	contract.Objective = strings.TrimSpace(contract.Objective)
	contract.Summary = strings.TrimSpace(contract.Summary)
	contract.SourceNextActionRecordIDs = normalizeStringList(contract.SourceNextActionRecordIDs)
	contract.AllowedActions = normalizeActionStringSlice(contract.AllowedActions)
	contract.ForbiddenActions = normalizeActionStringSlice(contract.ForbiddenActions)
	contract.StopConditions = normalizeStringList(contract.StopConditions)
	contract.PrimaryContinuationContractID = strings.TrimSpace(contract.PrimaryContinuationContractID)
	contract.RequiredCapabilityGrants = NormalizeCapabilityGrantSpecs(contract.RequiredCapabilityGrants)
	contract.Components = normalizeAuthorityBundleComponents(contract.Components)
	if !contract.ExpiresAt.IsZero() {
		contract.ExpiresAt = contract.ExpiresAt.UTC()
	}
	if !contract.CreatedAt.IsZero() {
		contract.CreatedAt = contract.CreatedAt.UTC()
	}
	if !contract.UpdatedAt.IsZero() {
		contract.UpdatedAt = contract.UpdatedAt.UTC()
	}
	return contract
}

func NormalizeAuthorityBundleStatus(status AuthorityBundleStatus) AuthorityBundleStatus {
	value := normalizeEnumValue(string(status))
	switch AuthorityBundleStatus(value) {
	case AuthorityBundleStatusRecorded, AuthorityBundleStatusSuperseded:
		return AuthorityBundleStatus(value)
	default:
		return AuthorityBundleStatusRecorded
	}
}

func normalizeAuthorityBundleContractInput(input AuthorityBundleContractInput) AuthorityBundleContractInput {
	input.RequestInstanceID = strings.TrimSpace(input.RequestInstanceID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Principal = strings.TrimSpace(input.Principal)
	input.Objective = strings.TrimSpace(input.Objective)
	input.Summary = strings.TrimSpace(input.Summary)
	input.SourceNextActionRecordIDs = normalizeStringList(input.SourceNextActionRecordIDs)
	input.AllowedActions = normalizeActionStringSlice(input.AllowedActions)
	input.ForbiddenActions = normalizeActionStringSlice(input.ForbiddenActions)
	input.StopConditions = normalizeStringList(input.StopConditions)
	input.PrimaryContinuationContractID = strings.TrimSpace(input.PrimaryContinuationContractID)
	input.RequiredCapabilityGrants = NormalizeCapabilityGrantSpecs(input.RequiredCapabilityGrants)
	input.Components = normalizeAuthorityBundleComponents(input.Components)
	if !input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.ExpiresAt.UTC()
	}
	if !input.CreatedAt.IsZero() {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	return input
}

func normalizeAuthorityBundleComponents(values []AuthorityBundleComponent) []AuthorityBundleComponent {
	out := make([]AuthorityBundleComponent, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value.Kind = normalizeEnumValue(value.Kind)
		value.RefID = strings.TrimSpace(value.RefID)
		value.Subject = normalizeEnumValue(value.Subject)
		value.SubjectRef = strings.TrimSpace(value.SubjectRef)
		if value.Kind == "" || value.RefID == "" {
			continue
		}
		key := value.Kind + "\x00" + value.RefID + "\x00" + value.Subject + "\x00" + value.SubjectRef
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validateAuthorityBundleComponents(values []AuthorityBundleComponent) error {
	for _, value := range values {
		if !validAuthorityBundleComponentKind(value.Kind) {
			return fmt.Errorf("authority bundle component kind %q is not registered", value.Kind)
		}
	}
	return nil
}

func validAuthorityBundleComponentKind(kind string) bool {
	switch normalizeEnumValue(kind) {
	case AuthorityBundleComponentKindCapabilityRequest,
		AuthorityBundleComponentKindContinuationRecoveryContract,
		AuthorityBundleComponentKindNextAction,
		AuthorityBundleComponentKindChildAuthorityBundle,
		AuthorityBundleComponentKindChildTaskResult:
		return true
	default:
		return false
	}
}

func authorityBundleContractHash(input AuthorityBundleContractInput) string {
	return authorityBundleContractHashForVersion(input, AuthorityBundleContractVersion)
}

func authorityBundleContractHashForVersion(input AuthorityBundleContractInput, version string) string {
	input = normalizeAuthorityBundleContractInput(input)
	if strings.TrimSpace(version) == AuthorityBundleContractVersionV1 {
		return authorityBundleContractHashV1(input)
	}
	payload := map[string]any{
		"contract_version":                 AuthorityBundleContractVersion,
		"session_id":                       input.SessionID,
		"principal":                        input.Principal,
		"objective":                        input.Objective,
		"summary":                          input.Summary,
		"source_next_action_record_ids":    input.SourceNextActionRecordIDs,
		"allowed_actions":                  input.AllowedActions,
		"forbidden_actions":                input.ForbiddenActions,
		"stop_conditions":                  input.StopConditions,
		"primary_continuation_contract_id": input.PrimaryContinuationContractID,
		"required_capability_grants":       input.RequiredCapabilityGrants,
		"components":                       input.Components,
		"expires_at":                       contractTimeHashValue(input.ExpiresAt),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func authorityBundleContractHashV1(input AuthorityBundleContractInput) string {
	input = normalizeAuthorityBundleContractInput(input)
	payload := map[string]any{
		"session_id":                       input.SessionID,
		"principal":                        input.Principal,
		"objective":                        input.Objective,
		"summary":                          input.Summary,
		"source_next_action_record_ids":    input.SourceNextActionRecordIDs,
		"allowed_actions":                  input.AllowedActions,
		"forbidden_actions":                input.ForbiddenActions,
		"stop_conditions":                  input.StopConditions,
		"primary_continuation_contract_id": input.PrimaryContinuationContractID,
		"required_capability_grants":       input.RequiredCapabilityGrants,
		"components":                       input.Components,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func contractTimeHashValue(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func authorityBundleContractID(requestInstanceID string, hash string) string {
	token := strings.TrimPrefix(strings.TrimSpace(hash), "sha256:")
	if len(token) > 24 {
		token = token[:24]
	}
	request := strings.TrimSpace(requestInstanceID)
	if request != "" {
		r := sha256.Sum256([]byte(request))
		requestToken := hex.EncodeToString(r[:])
		if len(requestToken) > 12 {
			requestToken = requestToken[:12]
		}
		return "authbundle-" + requestToken + "-" + token
	}
	return "authbundle-" + token
}
