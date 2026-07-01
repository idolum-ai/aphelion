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

func TestContinuationRecoveryContractRequiresSessionIDForNewContracts(t *testing.T) {
	t.Parallel()

	_, err := CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
		RequestInstanceID: "missing-session-instance",
		SubjectKind:       "continuation_lease_request",
		SubjectRef:        ContinuationRecoverySubjectRef(ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
		Principal:         "telegram:1001",
		LeaseClass:        ContinuationLeaseClassChildWake,
		AllowedActions:    []string{"wake_named_child"},
		Constraints:       map[string]string{"agent_id": "child-alpha"},
		Tool:              "durable_agent",
		ToolAction:        "wake_once",
		AgentID:           "child-alpha",
		CreatedAt:         time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "session_id") {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v, want session_id rejection", err)
	}
}

func TestContinuationRecoveryContractRejectsTamperedContractIdentityTerms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		tamper func(t *testing.T, store *SQLiteStore, contract ContinuationRecoveryContract)
	}{
		{
			name: "session_id",
			tamper: func(t *testing.T, store *SQLiteStore, contract ContinuationRecoveryContract) {
				t.Helper()
				if _, err := store.db.Exec(`UPDATE continuation_recovery_contracts SET session_id = ? WHERE contract_id = ?`, "telegram_dm:9999", contract.ContractID); err != nil {
					t.Fatalf("tamper session_id: %v", err)
				}
			},
		},
		{
			name: "subject_ref",
			tamper: func(t *testing.T, store *SQLiteStore, contract ContinuationRecoveryContract) {
				t.Helper()
				if _, err := store.db.Exec(`UPDATE continuation_recovery_contracts SET subject_ref = ? WHERE contract_id = ?`, "child_wake:child-beta:grant-child-beta-wake", contract.ContractID); err != nil {
					t.Fatalf("tamper subject_ref: %v", err)
				}
			},
		},
		{
			name: "retry_request_instance_id",
			tamper: func(t *testing.T, store *SQLiteStore, contract ContinuationRecoveryContract) {
				t.Helper()
				retry := contract.RetryOperation
				retry.RequestInstanceID = "different-request-instance"
				raw, err := json.Marshal(retry)
				if err != nil {
					t.Fatalf("marshal retry: %v", err)
				}
				if _, err := store.db.Exec(`UPDATE continuation_recovery_contracts SET retry_operation_json = ? WHERE contract_id = ?`, string(raw), contract.ContractID); err != nil {
					t.Fatalf("tamper retry request_instance_id: %v", err)
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newTestSQLiteStore(t)
			defer store.Close()
			contract, err := CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
				RequestInstanceID:   "identity-tamper-" + tc.name,
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
			if contract.ContractVersion != ContinuationRecoveryContractVersion {
				t.Fatalf("contract version = %q, want %q", contract.ContractVersion, ContinuationRecoveryContractVersion)
			}
			if _, err := store.UpsertContinuationRecoveryContract(contract); err != nil {
				t.Fatalf("UpsertContinuationRecoveryContract() err = %v", err)
			}
			tc.tamper(t, store, contract)
			if _, _, err := store.ContinuationRecoveryContract(contract.ContractID); err == nil || !strings.Contains(err.Error(), "mismatch") {
				t.Fatalf("ContinuationRecoveryContract() err = %v, want mismatch rejection", err)
			}
		})
	}
}

func TestContinuationRecoveryContractAcceptsLegacyEmptySessionID(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	input := normalizeContinuationRecoveryContractInput(ContinuationRecoveryContractInput{
		RequestInstanceID: "legacy-empty-session",
		SubjectKind:       "continuation_lease_request",
		SubjectRef:        ContinuationRecoverySubjectRef(ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
		Principal:         "telegram:1001",
		LeaseClass:        ContinuationLeaseClassChildWake,
		AllowedActions:    []string{"wake_named_child"},
		Constraints:       map[string]string{"agent_id": "child-alpha"},
		Tool:              "durable_agent",
		ToolAction:        "wake_once",
		AgentID:           "child-alpha",
		RetryOperation: ContinuationRetryOperation{
			Contract:          ContinuationRecoveryRetryVersion,
			OperationKind:     "durable_agent_wake_once",
			Tool:              "durable_agent",
			InputJSON:         `{"action":"wake_once","agent_id":"child-alpha"}`,
			SubjectKind:       "continuation_lease_request",
			SubjectRef:        ContinuationRecoverySubjectRef(ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
			RequestInstanceID: "legacy-empty-session",
		},
		CreatedAt: now,
	})
	hash := continuationRecoveryContractHashForVersion(input, ContinuationRecoveryContractVersionV1)
	legacy := ContinuationRecoveryContract{
		ContractID:        continuationRecoveryContractID(input.RequestInstanceID, hash),
		ContractVersion:   ContinuationRecoveryContractVersionV1,
		RequestInstanceID: input.RequestInstanceID,
		ContractHash:      hash,
		SubjectKind:       input.SubjectKind,
		SubjectRef:        input.SubjectRef,
		Status:            ContinuationRecoveryContractStatusRecorded,
		Principal:         input.Principal,
		LeaseClass:        input.LeaseClass,
		AllowedActions:    input.AllowedActions,
		Constraints:       input.Constraints,
		Tool:              input.Tool,
		ToolAction:        input.ToolAction,
		AgentID:           input.AgentID,
		RetryOperation:    input.RetryOperation,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	stored, err := store.UpsertContinuationRecoveryContract(legacy)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(legacy) err = %v", err)
	}
	if stored.ContractVersion != ContinuationRecoveryContractVersionV1 || stored.SessionID != "" {
		t.Fatalf("stored legacy = %#v, want v1 empty-session compatibility", stored)
	}
}

func TestContinuationRecoveryContractNextActionPublicationRollsBackOnHandoffFailure(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	contract, err := CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
		RequestInstanceID:   "atomic-publication-instance",
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

	key := SessionKey{ChatID: 1001, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1001"}}
	_, _, err = store.RecordContinuationRecoveryContractNextAction(contract, NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		RequiredAuthority:  string(ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: `{"action":"request_continuation_lease"`,
	})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("RecordContinuationRecoveryContractNextAction() err = %v, want invalid JSON failure", err)
	}
	if _, ok, err := store.ContinuationRecoveryContract(contract.ContractID); err != nil {
		t.Fatalf("ContinuationRecoveryContract() err = %v", err)
	} else if ok {
		t.Fatalf("ContinuationRecoveryContract(%q) ok=true, want rollback to remove orphan contract", contract.ContractID)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions = %#v, want no half-published handoff", open)
	}
}
