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
	ContinuationRecoveryContractVersion = "aphelion.continuation_recovery.v1"
	ContinuationRecoveryRetryVersion    = "aphelion.recovery_retry.v1"
)

type ContinuationRecoveryContractStatus string

const (
	ContinuationRecoveryContractStatusRecorded   ContinuationRecoveryContractStatus = "recorded"
	ContinuationRecoveryContractStatusTerminal   ContinuationRecoveryContractStatus = "terminal"
	ContinuationRecoveryContractStatusSuperseded ContinuationRecoveryContractStatus = "superseded"
)

type ContinuationRecoveryContract struct {
	ContractID          string                             `json:"contract_id"`
	ContractVersion     string                             `json:"contract_version"`
	RequestInstanceID   string                             `json:"request_instance_id"`
	ContractHash        string                             `json:"contract_hash"`
	SessionID           string                             `json:"session_id,omitempty"`
	SubjectKind         string                             `json:"subject_kind"`
	SubjectRef          string                             `json:"subject_ref"`
	Status              ContinuationRecoveryContractStatus `json:"status"`
	Principal           string                             `json:"principal"`
	LeaseClass          ContinuationLeaseClass             `json:"lease_class"`
	AllowedActions      []string                           `json:"allowed_actions,omitempty"`
	Constraints         map[string]string                  `json:"constraints,omitempty"`
	Tool                string                             `json:"tool,omitempty"`
	ToolAction          string                             `json:"tool_action,omitempty"`
	AgentID             string                             `json:"agent_id,omitempty"`
	Resource            string                             `json:"resource,omitempty"`
	GrantID             string                             `json:"grant_id,omitempty"`
	GrantTargetResource string                             `json:"grant_target_resource,omitempty"`
	RetryOperation      ContinuationRetryOperation         `json:"retry_operation,omitempty"`
	CreatedAt           time.Time                          `json:"created_at,omitempty"`
	UpdatedAt           time.Time                          `json:"updated_at,omitempty"`
}

type ContinuationRecoveryContractInput struct {
	RequestInstanceID   string
	SessionID           string
	SubjectKind         string
	SubjectRef          string
	Principal           string
	LeaseClass          ContinuationLeaseClass
	AllowedActions      []string
	Constraints         map[string]string
	Tool                string
	ToolAction          string
	AgentID             string
	Resource            string
	GrantID             string
	GrantTargetResource string
	RetryOperation      ContinuationRetryOperation
	CreatedAt           time.Time
}

func CompileContinuationRecoveryContract(input ContinuationRecoveryContractInput) (ContinuationRecoveryContract, error) {
	input = normalizeContinuationRecoveryContractInput(input)
	if input.RequestInstanceID == "" {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract requires request_instance_id")
	}
	if input.Principal == "" {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract requires principal")
	}
	if input.LeaseClass == "" {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract requires lease_class")
	}
	if len(input.AllowedActions) == 0 {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract requires allowed_actions")
	}
	if input.SubjectKind == "" {
		input.SubjectKind = "continuation_lease_request"
	}
	if input.SubjectRef == "" {
		input.SubjectRef = ContinuationRecoverySubjectRef(input.LeaseClass, input.AgentID, input.GrantID, input.Tool, input.ToolAction, input.Resource)
	}
	if input.SubjectRef == "" {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract requires subject_ref")
	}
	switch input.LeaseClass {
	case ContinuationLeaseClassChildWake:
		if input.AgentID == "" {
			return ContinuationRecoveryContract{}, fmt.Errorf("child_wake recovery contract requires agent_id")
		}
		if input.Tool != "durable_agent" || input.ToolAction != "wake_once" {
			return ContinuationRecoveryContract{}, fmt.Errorf("child_wake recovery contract must target durable_agent wake_once")
		}
		if !recoveryStringSliceContains(input.AllowedActions, "wake_named_child") {
			return ContinuationRecoveryContract{}, fmt.Errorf("child_wake recovery contract requires wake_named_child action")
		}
		if got := strings.TrimSpace(input.Constraints["agent_id"]); got != "" && got != input.AgentID {
			return ContinuationRecoveryContract{}, fmt.Errorf("child_wake recovery contract agent_id constraint mismatch")
		}
		input.Constraints["agent_id"] = input.AgentID
	case ContinuationLeaseClassDataAccess, ContinuationLeaseClassLocalWorkspace:
		if input.GrantID == "" || input.GrantTargetResource == "" || input.Resource == "" || input.Tool == "" || input.ToolAction == "" {
			return ContinuationRecoveryContract{}, fmt.Errorf("%s recovery contract requires grant, resource, tool, and action", input.LeaseClass)
		}
		required := map[string]string{
			"grant_id":              input.GrantID,
			"grant_target_resource": input.GrantTargetResource,
			"target_resource":       input.GrantTargetResource,
			"resource":              input.Resource,
			"tool":                  input.Tool,
			"tool_action":           input.ToolAction,
		}
		for key, want := range required {
			if got := strings.TrimSpace(input.Constraints[key]); got != "" && got != want {
				return ContinuationRecoveryContract{}, fmt.Errorf("%s recovery contract %s constraint mismatch", input.LeaseClass, key)
			}
			input.Constraints[key] = want
		}
	}
	input.RetryOperation = NormalizeContinuationRetryOperation(input.RetryOperation)
	if input.LeaseClass == ContinuationLeaseClassChildWake && !input.RetryOperation.Active() {
		input.RetryOperation = ContinuationRetryOperation{
			Contract:          ContinuationRecoveryRetryVersion,
			OperationKind:     "durable_agent_wake_once",
			Tool:              "durable_agent",
			InputJSON:         compactRecoveryJSON(map[string]any{"action": "wake_once", "agent_id": input.AgentID}),
			SubjectKind:       input.SubjectKind,
			SubjectRef:        input.SubjectRef,
			RequestInstanceID: input.RequestInstanceID,
		}
	}
	input.RetryOperation = normalizeContinuationRecoveryRetry(input)
	if err := validateContinuationRecoveryRetry(input); err != nil {
		return ContinuationRecoveryContract{}, err
	}
	hash := continuationRecoveryContractHash(input)
	id := continuationRecoveryContractID(input.RequestInstanceID, hash)
	now := input.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return NormalizeContinuationRecoveryContract(ContinuationRecoveryContract{
		ContractID:          id,
		ContractVersion:     ContinuationRecoveryContractVersion,
		RequestInstanceID:   input.RequestInstanceID,
		ContractHash:        hash,
		SessionID:           input.SessionID,
		SubjectKind:         input.SubjectKind,
		SubjectRef:          input.SubjectRef,
		Status:              ContinuationRecoveryContractStatusRecorded,
		Principal:           input.Principal,
		LeaseClass:          input.LeaseClass,
		AllowedActions:      input.AllowedActions,
		Constraints:         input.Constraints,
		Tool:                input.Tool,
		ToolAction:          input.ToolAction,
		AgentID:             input.AgentID,
		Resource:            input.Resource,
		GrantID:             input.GrantID,
		GrantTargetResource: input.GrantTargetResource,
		RetryOperation:      input.RetryOperation,
		CreatedAt:           now,
		UpdatedAt:           now,
	}), nil
}

func NormalizeContinuationRecoveryContract(contract ContinuationRecoveryContract) ContinuationRecoveryContract {
	contract.ContractID = strings.TrimSpace(contract.ContractID)
	contract.ContractVersion = strings.TrimSpace(contract.ContractVersion)
	if contract.ContractVersion == "" {
		contract.ContractVersion = ContinuationRecoveryContractVersion
	}
	contract.RequestInstanceID = strings.TrimSpace(contract.RequestInstanceID)
	contract.ContractHash = strings.TrimSpace(contract.ContractHash)
	contract.SessionID = strings.TrimSpace(contract.SessionID)
	contract.SubjectKind = normalizeEnumValue(contract.SubjectKind)
	if contract.SubjectKind == "" {
		contract.SubjectKind = "continuation_lease_request"
	}
	contract.SubjectRef = strings.TrimSpace(contract.SubjectRef)
	contract.Status = NormalizeContinuationRecoveryContractStatus(contract.Status)
	contract.Principal = strings.TrimSpace(contract.Principal)
	contract.LeaseClass = NormalizeContinuationLeaseClass(contract.LeaseClass)
	contract.AllowedActions = NormalizeCapabilityActions(contract.AllowedActions)
	contract.Constraints = normalizeRecoveryStringMap(contract.Constraints)
	contract.Tool = strings.TrimSpace(contract.Tool)
	contract.ToolAction = normalizeRecoveryAction(contract.ToolAction)
	contract.AgentID = strings.TrimSpace(contract.AgentID)
	contract.Resource = strings.TrimSpace(contract.Resource)
	contract.GrantID = strings.TrimSpace(contract.GrantID)
	contract.GrantTargetResource = strings.TrimSpace(contract.GrantTargetResource)
	contract.RetryOperation = NormalizeContinuationRetryOperation(contract.RetryOperation)
	if !contract.CreatedAt.IsZero() {
		contract.CreatedAt = contract.CreatedAt.UTC()
	}
	if !contract.UpdatedAt.IsZero() {
		contract.UpdatedAt = contract.UpdatedAt.UTC()
	}
	return contract
}

func NormalizeContinuationRecoveryContractStatus(status ContinuationRecoveryContractStatus) ContinuationRecoveryContractStatus {
	switch ContinuationRecoveryContractStatus(normalizeEnumValue(string(status))) {
	case ContinuationRecoveryContractStatusRecorded, ContinuationRecoveryContractStatusTerminal, ContinuationRecoveryContractStatusSuperseded:
		return ContinuationRecoveryContractStatus(normalizeEnumValue(string(status)))
	default:
		return ContinuationRecoveryContractStatusRecorded
	}
}

func ContinuationRecoveryContractProjectionInput(contractID string) string {
	raw, _ := json.Marshal(map[string]any{
		"action":                  "request_continuation_lease",
		"contract_id":             strings.TrimSpace(contractID),
		"recovery_contract":       "aphelion.recovery_handoff.v1",
		"recovery_operation_kind": "continuation_lease_request",
	})
	return string(raw)
}

func ContinuationRecoverySubjectRef(class ContinuationLeaseClass, agentID string, grantID string, tool string, toolAction string, resource string) string {
	class = NormalizeContinuationLeaseClass(class)
	parts := []string{string(class)}
	token := strings.TrimSpace(agentID)
	if token == "" {
		token = strings.TrimSpace(resource)
	}
	if token == "" {
		token = strings.TrimSpace(grantID)
	}
	if token == "" {
		return ""
	}
	parts = append(parts, token, strings.TrimSpace(grantID))
	tool = strings.TrimSpace(tool)
	if tool != "" {
		parts = append(parts, "tool="+tool)
	}
	toolAction = normalizeRecoveryAction(toolAction)
	if toolAction != "" {
		parts = append(parts, "action="+toolAction)
	}
	resource = strings.TrimSpace(resource)
	if resource != "" {
		parts = append(parts, "resource="+shortRecoveryHash(resource))
	}
	return strings.Join(parts, ":")
}

func normalizeContinuationRecoveryContractInput(input ContinuationRecoveryContractInput) ContinuationRecoveryContractInput {
	input.RequestInstanceID = strings.TrimSpace(input.RequestInstanceID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.SubjectKind = normalizeEnumValue(input.SubjectKind)
	input.SubjectRef = strings.TrimSpace(input.SubjectRef)
	input.Principal = strings.TrimSpace(input.Principal)
	input.LeaseClass = NormalizeContinuationLeaseClass(input.LeaseClass)
	input.AllowedActions = NormalizeCapabilityActions(input.AllowedActions)
	input.Constraints = normalizeRecoveryStringMap(input.Constraints)
	input.Tool = strings.TrimSpace(input.Tool)
	input.ToolAction = normalizeRecoveryAction(input.ToolAction)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.Resource = strings.TrimSpace(input.Resource)
	input.GrantID = strings.TrimSpace(input.GrantID)
	input.GrantTargetResource = strings.TrimSpace(input.GrantTargetResource)
	input.RetryOperation = NormalizeContinuationRetryOperation(input.RetryOperation)
	if !input.CreatedAt.IsZero() {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	return input
}

func normalizeContinuationRecoveryRetry(input ContinuationRecoveryContractInput) ContinuationRetryOperation {
	op := NormalizeContinuationRetryOperation(input.RetryOperation)
	if !op.Active() {
		return op
	}
	if op.Contract == "" {
		op.Contract = ContinuationRecoveryRetryVersion
	}
	if op.SubjectKind == "" {
		op.SubjectKind = input.SubjectKind
	}
	if op.SubjectRef == "" {
		op.SubjectRef = input.SubjectRef
	}
	if op.RequestInstanceID == "" {
		op.RequestInstanceID = input.RequestInstanceID
	}
	return NormalizeContinuationRetryOperation(op)
}

func validateContinuationRecoveryRetry(input ContinuationRecoveryContractInput) error {
	op := NormalizeContinuationRetryOperation(input.RetryOperation)
	if !op.Active() {
		return nil
	}
	if op.Contract != ContinuationRecoveryRetryVersion {
		return fmt.Errorf("continuation recovery retry contract must be %s", ContinuationRecoveryRetryVersion)
	}
	if op.RequestInstanceID != "" && op.RequestInstanceID != input.RequestInstanceID {
		return fmt.Errorf("continuation recovery retry request_instance_id mismatch")
	}
	if op.SubjectKind != "" && op.SubjectKind != input.SubjectKind {
		return fmt.Errorf("continuation recovery retry subject kind mismatch")
	}
	if op.SubjectRef != "" && op.SubjectRef != input.SubjectRef {
		return fmt.Errorf("continuation recovery retry subject ref mismatch")
	}
	switch input.LeaseClass {
	case ContinuationLeaseClassChildWake:
		if op.Tool != "durable_agent" || op.OperationKind != "durable_agent_wake_once" {
			return fmt.Errorf("child_wake recovery retry must invoke durable_agent wake_once")
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(op.InputJSON), &payload); err != nil {
			return fmt.Errorf("decode child_wake recovery retry input: %w", err)
		}
		if strings.TrimSpace(fmt.Sprint(payload["action"])) != "wake_once" ||
			strings.TrimSpace(fmt.Sprint(payload["agent_id"])) != input.AgentID {
			return fmt.Errorf("child_wake recovery retry must target exact agent_id")
		}
	default:
		return fmt.Errorf("%s recovery retry operation is not executable", input.LeaseClass)
	}
	return nil
}

func continuationRecoveryContractHash(input ContinuationRecoveryContractInput) string {
	input = normalizeContinuationRecoveryContractInput(input)
	payload := map[string]any{
		"agent_id":              input.AgentID,
		"resource":              input.Resource,
		"grant_id":              input.GrantID,
		"grant_target_resource": input.GrantTargetResource,
		"principal":             input.Principal,
		"lease_class":           string(input.LeaseClass),
		"allowed_actions":       input.AllowedActions,
		"constraints":           input.Constraints,
		"tool":                  input.Tool,
		"tool_action":           input.ToolAction,
	}
	if input.RetryOperation.Active() {
		retry := NormalizeContinuationRetryOperation(input.RetryOperation)
		payload["retry_operation"] = map[string]any{
			"contract":       retry.Contract,
			"operation_kind": retry.OperationKind,
			"tool":           retry.Tool,
			"input_json":     retry.InputJSON,
			"subject_kind":   retry.SubjectKind,
			"subject_ref":    retry.SubjectRef,
		}
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func continuationRecoveryContractID(requestInstanceID string, contractHash string) string {
	payload := map[string]any{
		"request_instance_id": strings.TrimSpace(requestInstanceID),
		"contract_hash":       strings.TrimSpace(contractHash),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	token := hex.EncodeToString(sum[:])
	if len(token) > 24 {
		token = token[:24]
	}
	return "crc-" + token
}

func normalizeRecoveryAction(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeRecoveryStringMap(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func recoveryStringSliceContains(values []string, want string) bool {
	want = normalizeRecoveryAction(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if normalizeRecoveryAction(value) == want {
			return true
		}
	}
	return false
}

func compactRecoveryJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func shortRecoveryHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	token := hex.EncodeToString(sum[:])
	if len(token) > 16 {
		return token[:16]
	}
	return token
}
