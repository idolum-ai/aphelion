//go:build linux

package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestDiscoveredEffectContractCompilesToContinuationRecoveryInput(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	command := "git fetch origin main"
	contract := core.DiscoveredEffectContract{
		ID:                    "effect_contract_01J",
		AgentID:               "release-child",
		SourceEffectRequestID: "effect_req_01J",
		ContractKind:          core.ExternalRuntimeContractKindExternalEffect,
		Provider:              "git",
		Action:                "fetch",
		ReviewRoute:           "authority_principal",
		Constraints: map[string]json.RawMessage{
			"command":      json.RawMessage(`"` + command + `"`),
			"workdir":      json.RawMessage(`"/srv/aphelion"`),
			"command_hash": json.RawMessage(`"` + EffectAttemptCommandHash(command) + `"`),
		},
		Materializes:   core.GrantMaterialization{LeaseKind: core.ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900, SingleUse: true},
		ExpectedResult: core.ExpectedEffectResult{Kind: "effect_result", ArtifactPolicy: "bounded_redacted_summary"},
		CreatedAt:      now,
	}
	input, err := CompileDiscoveredEffectContinuationRecoveryInput(DiscoveredEffectContinuationRecoveryInput{
		Contract:          contract,
		SessionID:         "telegram_dm:1001",
		RequestInstanceID: "effect-recovery-instance",
		Principal:         "telegram:1001",
		TimeoutSec:        30,
		CreatedAt:         now,
	})
	if err != nil {
		t.Fatalf("CompileDiscoveredEffectContinuationRecoveryInput() err = %v", err)
	}
	if input.SubjectKind != ContinuationRecoverySubjectKindDiscoveredEffect ||
		input.LeaseClass != ContinuationLeaseClassDataAccess ||
		input.Tool != "exec" ||
		input.ToolAction != ContinuationRecoveryRetryExecExactCommand {
		t.Fatalf("input = %#v, want discovered exact exec recovery input", input)
	}
	if input.Constraints["contract_kind"] != ContinuationRecoveryContractKindDiscoveredEffect ||
		input.Constraints["effect_provider"] != "git" ||
		input.Constraints["effect_action"] != "fetch" ||
		input.Constraints["command"] != command ||
		input.Constraints["workdir"] != "/srv/aphelion" ||
		input.Constraints["command_hash"] != EffectAttemptCommandHash(command) {
		t.Fatalf("constraints = %#v, want exact command/provider/workdir constraints", input.Constraints)
	}
	if !stringSliceContainsForBridgeTest(input.AllowedActions, "fetch") || !stringSliceContainsForBridgeTest(input.AllowedActions, "report_fetch_evidence") {
		t.Fatalf("allowed actions = %#v, want fetch evidence actions", input.AllowedActions)
	}
	if !strings.Contains(input.RetryOperation.InputJSON, `"command":"git fetch origin main"`) ||
		!strings.Contains(input.RetryOperation.InputJSON, `"workdir":"/srv/aphelion"`) ||
		!strings.Contains(input.RetryOperation.InputJSON, `"timeout_sec":30`) {
		t.Fatalf("retry input = %q, want exact exec retry payload", input.RetryOperation.InputJSON)
	}
	compiled, err := CompileContinuationRecoveryContract(input)
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	if !ContinuationRecoveryContractIsDiscoveredEffect(compiled) {
		t.Fatalf("compiled contract = %#v, want discovered effect recovery contract", compiled)
	}
}

func TestDiscoveredEffectContractBridgeRejectsCommandHashMismatch(t *testing.T) {
	t.Parallel()

	_, err := CompileDiscoveredEffectContinuationRecoveryInput(DiscoveredEffectContinuationRecoveryInput{
		Contract: core.DiscoveredEffectContract{
			ID:                    "effect_contract_bad_hash",
			AgentID:               "release-child",
			SourceEffectRequestID: "effect_req_bad_hash",
			ContractKind:          core.ExternalRuntimeContractKindExternalEffect,
			Provider:              "git",
			Action:                "fetch",
			ReviewRoute:           "authority_principal",
			Constraints: map[string]json.RawMessage{
				"command":      json.RawMessage(`"git fetch origin main"`),
				"command_hash": json.RawMessage(`"sha256:not-the-command"`),
			},
			Materializes: core.GrantMaterialization{LeaseKind: core.ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900},
		},
		SessionID:         "telegram_dm:1001",
		RequestInstanceID: "effect-recovery-bad-hash",
		Principal:         "telegram:1001",
	})
	if err == nil || !strings.Contains(err.Error(), "command_hash mismatch") {
		t.Fatalf("CompileDiscoveredEffectContinuationRecoveryInput() err = %v, want command_hash mismatch", err)
	}
}

func TestDiscoveredEffectContractBridgeRejectsMissingCommand(t *testing.T) {
	t.Parallel()

	_, err := CompileDiscoveredEffectContinuationRecoveryInput(DiscoveredEffectContinuationRecoveryInput{
		Contract: core.DiscoveredEffectContract{
			ID:                    "effect_contract_missing_command",
			AgentID:               "release-child",
			SourceEffectRequestID: "effect_req_missing_command",
			ContractKind:          core.ExternalRuntimeContractKindExternalEffect,
			Provider:              "git",
			Action:                "fetch",
			ReviewRoute:           "authority_principal",
			Constraints:           map[string]json.RawMessage{"query": json.RawMessage(`"refs/heads/main"`)},
			Materializes:          core.GrantMaterialization{LeaseKind: core.ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900},
		},
		SessionID:         "telegram_dm:1001",
		RequestInstanceID: "effect-recovery-missing-command",
		Principal:         "telegram:1001",
	})
	if err == nil || !strings.Contains(err.Error(), "exact command") {
		t.Fatalf("CompileDiscoveredEffectContinuationRecoveryInput() err = %v, want exact command rejection", err)
	}
}

func stringSliceContainsForBridgeTest(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
