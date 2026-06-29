//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestRecoveryApprovalNextActionAcceptsAuthorityBundleRequest(t *testing.T) {
	t.Parallel()

	action := session.NextActionRecord{
		State:           session.NextActionBlockedNeedsAuthority,
		SubjectKind:     "authority_bundle_request",
		SubjectRef:      "authbundle-test",
		ResourceBlocker: "authority_bundle_approval",
		OperationKind:   "authority_bundle_request",
		OperationTool:   "request_approval",
		OperationInputJSON: `{
			"action":"request_authority_bundle",
			"contract_id":"authbundle-test",
			"recovery_contract":"aphelion.recovery_handoff.v1",
			"recovery_operation_kind":"authority_bundle_request"
		}`,
		CreatedAt: time.Now().UTC(),
	}
	consumable, invalid := recoveryApprovalNextActionConsumable(action)
	if !consumable || invalid {
		t.Fatalf("recoveryApprovalNextActionConsumable(authority bundle) = (%v, %v), want (true, false)", consumable, invalid)
	}

	action.SubjectKind = "continuation_lease_request"
	consumable, invalid = recoveryApprovalNextActionConsumable(action)
	if consumable || invalid {
		t.Fatalf("recoveryApprovalNextActionConsumable(wrong subject) = (%v, %v), want ignored non-invalid", consumable, invalid)
	}

	action.SubjectKind = "authority_bundle_request"
	action.OperationInputJSON = `{"action":"request_authority_bundle","contract_id":"authbundle-test"}`
	consumable, invalid = recoveryApprovalNextActionConsumable(action)
	if consumable || !invalid {
		t.Fatalf("recoveryApprovalNextActionConsumable(missing envelope) = (%v, %v), want invalid", consumable, invalid)
	}
}
