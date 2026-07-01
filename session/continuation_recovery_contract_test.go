//go:build linux

package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestContinuationRecoveryContractLoadCanonicalizesDerivedRetryFields(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	contract, err := CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
		RequestInstanceID:   "canonical-retry-fields-instance",
		SessionID:           "telegram_dm:1001",
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          ContinuationRecoverySubjectRef(ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		RetryOperation: ContinuationRetryOperation{
			Contract:      ContinuationRecoveryRetryVersion,
			OperationKind: "durable_agent_wake_once",
			Tool:          "durable_agent",
			InputJSON:     `{"action":"wake_once","agent_id":"idolum-email"}`,
			SubjectKind:   "continuation_lease_request",
		},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	if _, err := store.UpsertContinuationRecoveryContract(contract); err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract() err = %v", err)
	}

	legacyRetry := contract.RetryOperation
	legacyRetry.SubjectRef = ""
	legacyRetry.RequestInstanceID = ""
	legacyRaw, err := json.Marshal(legacyRetry)
	if err != nil {
		t.Fatalf("Marshal legacy retry err = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE continuation_recovery_contracts SET retry_operation_json = ? WHERE contract_id = ?`, string(legacyRaw), contract.ContractID); err != nil {
		t.Fatalf("strip derived retry fields: %v", err)
	}

	loaded, ok, err := store.ContinuationRecoveryContract(contract.ContractID)
	if err != nil {
		t.Fatalf("ContinuationRecoveryContract() err = %v", err)
	}
	if !ok {
		t.Fatalf("ContinuationRecoveryContract(%q) ok=false", contract.ContractID)
	}
	if loaded.ContractHash != contract.ContractHash || loaded.ContractID != contract.ContractID {
		t.Fatalf("loaded contract identity = (%s, %s), want (%s, %s)", loaded.ContractID, loaded.ContractHash, contract.ContractID, contract.ContractHash)
	}
	if loaded.RetryOperation.SubjectRef != contract.SubjectRef || loaded.RetryOperation.RequestInstanceID != contract.RequestInstanceID {
		t.Fatalf("loaded retry = %#v, want derived subject_ref/request_instance_id restored", loaded.RetryOperation)
	}
}

func TestContinuationRecoveryContractRejectsTamperedStoredFields(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	contract, err := CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
		RequestInstanceID:   "canonical-tamper-instance",
		SessionID:           "telegram_dm:1001",
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          ContinuationRecoverySubjectRef(ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "child-alpha"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "child-alpha",
		GrantID:             "grant-child-alpha-wake",
		GrantTargetResource: "durable_agent:child-alpha:wake_once",
		CreatedAt:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	if _, err := store.UpsertContinuationRecoveryContract(contract); err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract() err = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE continuation_recovery_contracts SET agent_id = ? WHERE contract_id = ?`, "child-beta", contract.ContractID); err != nil {
		t.Fatalf("tamper contract agent: %v", err)
	}

	if _, _, err := store.ContinuationRecoveryContract(contract.ContractID); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("ContinuationRecoveryContract() err = %v, want mismatch rejection", err)
	}
}
