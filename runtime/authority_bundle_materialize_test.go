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

func TestRecoveryApprovalAuthorityBundleExecutableAcceptsGrantOnlyBundle(t *testing.T) {
	t.Parallel()

	_, store, _, _ := buildRuntimeFixtures(t)
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1004, Scope: telegramDMScopeRef(1004)}
	bundleID := seedGrantOnlyAuthorityBundleHandoff(t, store, key, "materialize-executable")
	actions, err := store.OpenNextActionsBySessionOperation(key, session.NextActionBlockedNeedsAuthority, "request_approval", "authority_bundle_request", 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionOperation() err = %v", err)
	}
	if len(actions) != 1 || actions[0].SubjectRef != bundleID {
		t.Fatalf("actions = %#v, want one grant-only authority bundle handoff", actions)
	}

	executable, invalid, terminalReason, err := rt.recoveryApprovalAuthorityBundleExecutable(key, actions[0], time.Now().UTC())
	if err != nil {
		t.Fatalf("recoveryApprovalAuthorityBundleExecutable() err = %v", err)
	}
	if !executable || invalid || terminalReason != "" {
		t.Fatalf("recoveryApprovalAuthorityBundleExecutable(grant-only) = executable=%v invalid=%v reason=%q, want executable grant-only bundle", executable, invalid, terminalReason)
	}
}

func TestRecoveryApprovalAuthorityBundleExecutableRejectsWrongSession(t *testing.T) {
	t.Parallel()

	_, store, _, _ := buildRuntimeFixtures(t)
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1005, Scope: telegramDMScopeRef(1005)}
	bundleID := seedGrantOnlyAuthorityBundleHandoff(t, store, key, "wrong-session")
	actions, err := store.OpenNextActionsBySessionOperation(key, session.NextActionBlockedNeedsAuthority, "request_approval", "authority_bundle_request", 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionOperation() err = %v", err)
	}
	if len(actions) != 1 || actions[0].SubjectRef != bundleID {
		t.Fatalf("actions = %#v, want one grant-only authority bundle handoff", actions)
	}

	wrongKey := session.SessionKey{ChatID: 1006, Scope: telegramDMScopeRef(1006)}
	executable, invalid, terminalReason, err := rt.recoveryApprovalAuthorityBundleExecutable(wrongKey, actions[0], time.Now().UTC())
	if err != nil {
		t.Fatalf("recoveryApprovalAuthorityBundleExecutable(wrong session) err = %v", err)
	}
	if executable || !invalid || terminalReason != "invalid_authority_bundle_handoff" {
		t.Fatalf("recoveryApprovalAuthorityBundleExecutable(wrong session) = executable=%v invalid=%v reason=%q, want invalid session-bound handoff", executable, invalid, terminalReason)
	}
}
