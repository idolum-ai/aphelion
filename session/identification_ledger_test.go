//go:build linux

package session

import (
	"strings"
	"testing"
	"time"
)

func TestIdentificationLedgerPreservesGraduatedObservationHistory(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	entryInput := IdentificationLedgerEntryInput{
		PlanID:      "plan-job-search",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		StepRef:     "step:read-unread-mail",
		ShapeHash:   "sha256:mail-shape",
		Status:      IdentificationLedgerStatusPartial,
	}
	if _, _, err := store.RecordIdentificationLedgerObservation(entryInput, IdentificationLedgerObservationInput{
		Method:      IdentificationObservationStatic,
		Property:    IdentificationPropertyApprovalClass,
		Value:       "data_access",
		EvidenceRef: "plan:static-analysis",
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerObservation(static) err = %v", err)
	}
	if _, _, err := store.RecordIdentificationLedgerObservation(entryInput, IdentificationLedgerObservationInput{
		Method:      IdentificationObservationCollision,
		Property:    IdentificationPropertyRetryability,
		Value:       "bounded_backoff",
		EvidenceRef: "next_action:mail-read-blocker",
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerObservation(collision) err = %v", err)
	}
	entryInput.LabelRef = "crc-mail-read"
	entryInput.Status = IdentificationLedgerStatusProposed
	if _, _, err := store.RecordIdentificationLedgerObservation(entryInput, IdentificationLedgerObservationInput{
		Method:      IdentificationObservationOperator,
		Property:    IdentificationPropertyContract,
		Value:       "crc-mail-read",
		EvidenceRef: "review_event:42",
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerObservation(operator) err = %v", err)
	}
	if _, _, err := store.RecordIdentificationLedgerObservation(entryInput, IdentificationLedgerObservationInput{
		Method:      IdentificationObservationCollision,
		Property:    IdentificationPropertyRetryability,
		Value:       "bounded_backoff",
		EvidenceRef: "next_action:mail-read-blocker",
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerObservation(duplicate) err = %v", err)
	}

	projections, err := store.IdentificationLedgerEntries(IdentificationLedgerQuery{
		PlanID:      "plan-job-search",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	if len(projections) != 1 {
		t.Fatalf("entries = %d, want 1", len(projections))
	}
	projection := projections[0]
	if projection.Entry.Status != IdentificationLedgerStatusProposed || projection.Entry.LabelRef != "crc-mail-read" {
		t.Fatalf("entry status/label = %q/%q, want proposed/crc-mail-read", projection.Entry.Status, projection.Entry.LabelRef)
	}
	if len(projection.Observations) != 3 {
		t.Fatalf("observations = %#v, want 3 append-only observations", projection.Observations)
	}
	if got := projection.Properties[IdentificationPropertyApprovalClass][0].Method; got != IdentificationObservationStatic {
		t.Fatalf("approval_class method = %q, want static", got)
	}
	if got := projection.Properties[IdentificationPropertyRetryability][0].Method; got != IdentificationObservationCollision {
		t.Fatalf("retryability method = %q, want collision", got)
	}
	if got := projection.Properties[IdentificationPropertyContract][0].Method; got != IdentificationObservationOperator {
		t.Fatalf("contract method = %q, want operator", got)
	}
}

func TestContinuationRecoveryPublicationRecordsIdentificationLedgerCollision(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	createdAt := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	contract, err := CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
		RequestInstanceID:   "ident-child-wake-instance",
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
		CreatedAt:           createdAt,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	key := SessionKey{ChatID: 1001, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1001"}}
	_, record, err := store.RecordContinuationRecoveryContractNextAction(contract, NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		CausalRefs:         []string{"test:collision"},
		RequiredAuthority:  string(ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: ContinuationRecoveryContractProjectionInput(contract.ContractID),
		CreatedAt:          createdAt,
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction() err = %v", err)
	}
	projections, err := store.IdentificationLedgerEntries(IdentificationLedgerQuery{
		PlanID:      IdentificationPlanIDForSession(contract.SessionID),
		PlanVersion: IdentificationDefaultPlanVersion,
		SessionID:   contract.SessionID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	if len(projections) != 1 {
		t.Fatalf("entries = %#v, want one collision-derived label", projections)
	}
	projection := projections[0]
	if projection.Entry.LabelRef != contract.ContractID || projection.Entry.ShapeHash != contract.ContractHash {
		t.Fatalf("entry label/hash = %q/%q, want %q/%q", projection.Entry.LabelRef, projection.Entry.ShapeHash, contract.ContractID, contract.ContractHash)
	}
	if got := projection.Properties[IdentificationPropertyApprovalClass][0].Value; got != string(ContinuationLeaseClassChildWake) {
		t.Fatalf("approval class = %q, want child_wake", got)
	}
	if got := projection.Properties[IdentificationPropertyContract][0].Value; got != contract.ContractID {
		t.Fatalf("contract property = %q, want %q", got, contract.ContractID)
	}
	foundEvidence := false
	for _, observation := range projection.Observations {
		if observation.EvidenceRef == "next_action:"+record.RecordID {
			foundEvidence = true
		}
		if strings.Contains(observation.Value, "idolum-email") && observation.Property == IdentificationPropertyResource {
			foundEvidence = true
		}
	}
	if !foundEvidence {
		t.Fatalf("observations = %#v, want next_action evidence/resource observation", projection.Observations)
	}
}
