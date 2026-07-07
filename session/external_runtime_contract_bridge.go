//go:build linux

package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type DiscoveredEffectContinuationRecoveryInput struct {
	Contract          core.DiscoveredEffectContract
	SessionID         string
	RequestInstanceID string
	Principal         string
	Command           string
	Workdir           string
	TimeoutSec        int
	CreatedAt         time.Time
}

// CompileDiscoveredEffectContinuationRecoveryInput adapts the generic external
// runtime discovered-effect contract into the existing exact continuation
// recovery contract. The returned value is still only a request contract; a
// continuation lease must be approved/materialized before execution.
func CompileDiscoveredEffectContinuationRecoveryInput(input DiscoveredEffectContinuationRecoveryInput) (ContinuationRecoveryContractInput, error) {
	contract := core.NormalizeDiscoveredEffectContract(input.Contract)
	if err := core.ValidateDiscoveredEffectContract(contract); err != nil {
		return ContinuationRecoveryContractInput{}, err
	}
	sessionID := strings.TrimSpace(input.SessionID)
	requestInstanceID := strings.TrimSpace(input.RequestInstanceID)
	principal := strings.TrimSpace(input.Principal)
	if sessionID == "" || requestInstanceID == "" || principal == "" {
		return ContinuationRecoveryContractInput{}, fmt.Errorf("discovered effect recovery input requires session_id, request_instance_id, and principal")
	}
	command := firstNonEmptyRecovery(strings.TrimSpace(input.Command), rawConstraintString(contract.Constraints["command"]))
	if command == "" {
		return ContinuationRecoveryContractInput{}, fmt.Errorf("discovered effect recovery input requires exact command constraint")
	}
	workdir := firstNonEmptyRecovery(strings.TrimSpace(input.Workdir), rawConstraintString(contract.Constraints["workdir"]))
	commandHash := EffectAttemptCommandHash(command)
	if want := rawConstraintString(contract.Constraints["command_hash"]); want != "" && want != commandHash {
		return ContinuationRecoveryContractInput{}, fmt.Errorf("discovered effect recovery command_hash mismatch")
	}
	constraints := map[string]string{
		"contract_kind": sessionDiscoveredEffectContractKind(),
		"effect_kind":   "network_or_external_contact",
		"effect_action": contract.Action,
		"command":       command,
		"command_hash":  commandHash,
	}
	if provider := strings.TrimSpace(contract.Provider); provider != "" {
		constraints["effect_provider"] = provider
	}
	if workdir != "" {
		constraints["workdir"] = workdir
	}
	retryPayload := map[string]any{"command": command}
	if workdir != "" {
		retryPayload["workdir"] = workdir
	}
	if input.TimeoutSec > 0 {
		retryPayload["timeout_sec"] = input.TimeoutSec
	}
	retryInput := string(mustMarshalRecoveryJSON(retryPayload))
	allowedActions := NormalizeCapabilityActions([]string{contract.Action, "report_evidence"})
	if contract.Action == "fetch" {
		allowedActions = NormalizeCapabilityActions(append(allowedActions, "report_fetch_evidence"))
	}
	out := ContinuationRecoveryContractInput{
		RequestInstanceID: requestInstanceID,
		SessionID:         sessionID,
		SubjectKind:       ContinuationRecoverySubjectKindDiscoveredEffect,
		Principal:         principal,
		LeaseClass:        ContinuationLeaseClassDataAccess,
		AllowedActions:    allowedActions,
		Constraints:       constraints,
		Tool:              "exec",
		ToolAction:        ContinuationRecoveryRetryExecExactCommand,
		AgentID:           strings.TrimSpace(contract.AgentID),
		Resource:          "command:" + commandHash,
		RetryOperation: ContinuationRetryOperation{
			Contract:          ContinuationRecoveryRetryVersion,
			OperationKind:     ContinuationRecoveryRetryExecExactCommand,
			Tool:              "exec",
			InputJSON:         retryInput,
			SubjectKind:       ContinuationRecoverySubjectKindDiscoveredEffect,
			RequestInstanceID: requestInstanceID,
		},
		CreatedAt: input.CreatedAt,
	}
	out.SubjectRef = ContinuationRecoverySubjectRef(out.LeaseClass, out.AgentID, out.GrantID, out.Tool, out.ToolAction, out.Resource)
	return out, nil
}

func sessionDiscoveredEffectContractKind() string {
	return ContinuationRecoveryContractKindDiscoveredEffect
}

func rawConstraintString(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}
