//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestAuthorityBundleProposesFromOpenTypedBlockersAndMaterializesApproval(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99101, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract := authorityBundleChildWakeRecoveryContract(t, store, key, "mail-child", "bundle-wake-request-1")
	handoff := json.RawMessage(session.ContinuationRecoveryContractProjectionInput(contract.ContractID))
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		NextAction:         "approve one child wake continuation before retrying durable_agent.wake_once",
		RequiredAuthority:  "continuation_lease",
		ResourceBlocker:    "missing_child_wake_lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      requestApprovalToolName,
		OperationInputJSON: string(handoff),
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordNextAction(child_wake blocker) err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, authorityBundleToolName, json.RawMessage(`{
		"action":"propose",
		"request_instance_id":"bundle-instance-1",
		"objective":"Finish one child setup/report cycle.",
		"summary":"Wake mail-child, verify readiness, and stop after one result.",
		"include_open_authority_blockers":true,
		"allowed_actions":["wake_named_child","inspect_child_runtime","report_result"],
		"forbidden_actions":["credentials_or_tokens","send_mail","unbounded_retry_loop"],
		"stop_conditions":["stop after one child result","stop on any typed blocker"]
	}`))
	if err != nil {
		t.Fatalf("authority_bundle propose err = %v", err)
	}
	if !strings.Contains(out, "[AUTHORITY_BUNDLE_PROPOSED]") || !strings.Contains(out, "primary_continuation_contract_id: "+contract.ContractID) {
		t.Fatalf("authority_bundle output = %q, want proposed bundle tied to continuation contract", out)
	}

	bundleAction := authorityBundleOpenAction(t, store, key)
	if bundleAction.OperationTool != requestApprovalToolName || bundleAction.OperationKind != "authority_bundle_request" {
		t.Fatalf("bundle action = %#v, want request_approval authority_bundle_request", bundleAction)
	}
	if err := ValidateRecoveryHandoffToolInput(bundleAction.State, bundleAction.OperationTool, bundleAction.OperationInputJSON); err != nil {
		t.Fatalf("ValidateRecoveryHandoffToolInput(bundle) err = %v", err)
	}
	var request requestApprovalInput
	if err := decodeToolObjectInput(json.RawMessage(bundleAction.OperationInputJSON), &request, requestApprovalToolName); err != nil {
		t.Fatalf("decode bundle request_approval err = %v", err)
	}
	bundle, ok, err := store.AuthorityBundleContract(request.ContractID)
	if err != nil {
		t.Fatalf("AuthorityBundleContract(%q) err = %v", request.ContractID, err)
	}
	if !ok {
		t.Fatalf("AuthorityBundleContract(%q) ok=false", request.ContractID)
	}
	if bundle.PrimaryContinuationContractID != contract.ContractID || len(bundle.RequiredCapabilityGrants) != 1 || bundle.RequiredCapabilityGrants[0].GrantID != "grant-mail-child-wake" {
		t.Fatalf("bundle = %#v, want primary child wake contract and exact required grant", bundle)
	}

	approval, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, requestApprovalToolName, json.RawMessage(bundleAction.OperationInputJSON))
	if err != nil {
		t.Fatalf("request_approval authority bundle err = %v", err)
	}
	if !strings.Contains(approval, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval request", approval)
	}
	continuation, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if continuation.ActionProposal.RiskClass != "authority_bundle" || !strings.Contains(continuation.ActionProposal.BoundedEffect, "credentials_or_tokens") {
		t.Fatalf("continuation proposal = %#v, want authority bundle boundaries", continuation.ActionProposal)
	}
	if continuation.ContinuationLease.Constraints["authority_bundle_id"] != bundle.BundleID {
		t.Fatalf("continuation constraints = %#v, want authority bundle id %s", continuation.ContinuationLease.Constraints, bundle.BundleID)
	}
	if len(continuation.ContinuationLease.RequiredCapabilityGrants) == 0 || continuation.ContinuationLease.RequiredCapabilityGrants[0].GrantID != "grant-mail-child-wake" {
		t.Fatalf("continuation required grants = %#v, want child wake grant", continuation.ContinuationLease.RequiredCapabilityGrants)
	}

	shown, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, authorityBundleToolName, json.RawMessage(`{"action":"show","bundle_id":"`+bundle.BundleID+`"}`))
	if err != nil {
		t.Fatalf("authority_bundle show err = %v", err)
	}
	if !strings.Contains(shown, "[AUTHORITY_BUNDLE]") || !strings.Contains(shown, bundle.BundleID) {
		t.Fatalf("show output = %q, want durable bundle projection", shown)
	}
}

func TestAuthorityBundleRejectsUnboundedOneTimeApproval(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99102, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract := authorityBundleChildWakeRecoveryContract(t, store, key, "mail-child", "bundle-wake-request-2")
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, authorityBundleToolName, json.RawMessage(`{
		"action":"propose",
		"request_instance_id":"bundle-instance-2",
		"summary":"Wake mail-child once.",
		"primary_continuation_contract_id":"`+contract.ContractID+`",
		"allowed_actions":["wake_named_child"],
		"forbidden_actions":["credentials_or_tokens"]
	}`))
	if err == nil || !strings.Contains(err.Error(), "stop_conditions") {
		t.Fatalf("authority_bundle propose output=%q err=%v, want stop_conditions rejection", out, err)
	}
}

func TestAuthorityBundleSkipsGrantSpecsAlreadyCoveredByActiveGrant(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99103, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract := authorityBundleChildWakeRecoveryContract(t, store, key, "mail-child", "bundle-wake-request-3")
	handoff := json.RawMessage(session.ContinuationRecoveryContractProjectionInput(contract.ContractID))
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		NextAction:         "approve one child wake continuation before retrying durable_agent.wake_once",
		RequiredAuthority:  "continuation_lease",
		ResourceBlocker:    "missing_child_wake_lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      requestApprovalToolName,
		OperationInputJSON: string(handoff),
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordNextAction(child_wake blocker) err = %v", err)
	}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-mail-read",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "mailbox:host@example.test",
		Purpose:        "Read only mailbox metadata for one bounded report.",
		ReviewStatus:   session.CapabilityReviewStatusApproved,
		GrantID:        "grant-mail-read",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest(active covered request) err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-mail-read",
		RequestID:      "cap-mail-read",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "mailbox:host@example.test",
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(active covered request) err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "capability_request",
		SubjectRef:         "cap-mail-read",
		NextAction:         "approve mailbox read grant",
		RequiredAuthority:  "external_account",
		ResourceBlocker:    "missing_grant",
		OperationKind:      "capability_grant_review",
		OperationTool:      "capability_authority",
		OperationInputJSON: `{"action":"grant_set","request_id":"cap-mail-read","allowed_actions":["read"]}`,
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(active covered grant blocker) err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, authorityBundleToolName, json.RawMessage(`{
		"action":"propose",
		"request_instance_id":"bundle-instance-3",
		"objective":"Finish one child setup/report cycle.",
		"summary":"Wake mail-child and use already-approved mailbox read authority.",
		"include_open_authority_blockers":true,
		"allowed_actions":["wake_named_child","read_mail_metadata","report_result"],
		"forbidden_actions":["credentials_or_tokens","send_mail","unbounded_retry_loop"],
		"stop_conditions":["stop after one child result","stop on any typed blocker"]
	}`))
	if err != nil {
		t.Fatalf("authority_bundle propose err = %v", err)
	}
	if !strings.Contains(out, "[AUTHORITY_BUNDLE_PROPOSED]") {
		t.Fatalf("authority_bundle output = %q, want proposed bundle", out)
	}
	bundleAction := authorityBundleOpenAction(t, store, key)
	var request requestApprovalInput
	if err := decodeToolObjectInput(json.RawMessage(bundleAction.OperationInputJSON), &request, requestApprovalToolName); err != nil {
		t.Fatalf("decode bundle request_approval err = %v", err)
	}
	bundle, ok, err := store.AuthorityBundleContract(request.ContractID)
	if err != nil {
		t.Fatalf("AuthorityBundleContract(%q) err = %v", request.ContractID, err)
	}
	if !ok {
		t.Fatalf("AuthorityBundleContract(%q) ok=false", request.ContractID)
	}
	if len(bundle.RequiredCapabilityGrants) != 1 || bundle.RequiredCapabilityGrants[0].GrantID != "grant-mail-child-wake" {
		t.Fatalf("bundle required grants = %#v, want only uncovered child_wake grant", bundle.RequiredCapabilityGrants)
	}
	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, requestApprovalToolName, json.RawMessage(bundleAction.OperationInputJSON)); err != nil {
		t.Fatalf("request_approval authority bundle err = %v", err)
	}
	continuation, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if len(continuation.ContinuationLease.RequiredCapabilityGrants) != 1 || continuation.ContinuationLease.RequiredCapabilityGrants[0].GrantID != "grant-mail-child-wake" {
		t.Fatalf("continuation required grants = %#v, want only uncovered child_wake grant", continuation.ContinuationLease.RequiredCapabilityGrants)
	}
}

func TestAuthorityBundleRejectsPartialRequiredGrantSpecBeforeApprovalCard(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99104, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, authorityBundleToolName, json.RawMessage(`{
		"action":"propose",
		"request_instance_id":"bundle-instance-4",
		"objective":"Exercise malformed bundle grant input.",
		"summary":"This bundle has a partial grant spec and must fail before materialization.",
		"allowed_actions":["inspect"],
		"forbidden_actions":["credentials_or_tokens"],
		"stop_conditions":["stop immediately on invalid contract"],
		"required_capability_grants":[{"allowed_actions":["read"]}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "required capability grant spec") || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("authority_bundle propose output=%q err=%v, want incomplete grant spec rejection", out, err)
	}
}

func authorityBundleChildWakeRecoveryContract(t *testing.T, store *session.SQLiteStore, key session.SessionKey, agentID string, requestInstanceID string) session.ContinuationRecoveryContract {
	t.Helper()

	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   requestInstanceID,
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, agentID, "grant-"+agentID+"-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": agentID},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             agentID,
		GrantID:             "grant-" + agentID + "-wake",
		GrantTargetResource: "durable_agent:" + agentID + ":wake_once",
		CreatedAt:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(child_wake) err = %v", err)
	}
	contract, err = store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(child_wake) err = %v", err)
	}
	return contract
}

func authorityBundleOpenAction(t *testing.T, store *session.SQLiteStore, key session.SessionKey) session.NextActionRecord {
	t.Helper()

	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.SubjectKind == "authority_bundle_request" {
			return action
		}
	}
	t.Fatalf("open actions = %#v, want authority_bundle_request", open)
	return session.NextActionRecord{}
}
