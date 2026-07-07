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

func TestAuthorityBundleOpenBlockerSweepDoesNotIncludeUnrelatedCapabilityRequests(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99107, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract := authorityBundleChildWakeRecoveryContract(t, store, key, "mail-child", "bundle-wake-request-unrelated")
	recordAuthorityBundleContinuationLeaseBlocker(t, store, key, contract)
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-unrelated-email",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "mailbox:unrelated@example.test",
		Purpose:        "Unrelated mailbox wake investigation from an older blocker.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
		GrantID:        "grant-unrelated-email",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest(unrelated) err = %v", err)
	}
	unrelatedAction, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "capability_request",
		SubjectRef:         "cap-unrelated-email",
		NextAction:         "approve unrelated mailbox grant",
		RequiredAuthority:  "external_account",
		ResourceBlocker:    "missing_grant",
		OperationKind:      "capability_grant_review",
		OperationTool:      "capability_authority",
		OperationInputJSON: `{"action":"grant_set","request_id":"cap-unrelated-email","allowed_actions":["read"]}`,
		CreatedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordNextAction(unrelated grant blocker) err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, authorityBundleToolName, json.RawMessage(`{
		"action":"propose",
		"request_instance_id":"bundle-instance-unrelated-open-blockers",
		"objective":"Finish one child wake without bundling old session authority.",
		"summary":"Wake mail-child and stop after one result.",
		"include_open_authority_blockers":true,
		"allowed_actions":["wake_named_child","report_result"],
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
	if authorityBundleContains(bundle.SourceNextActionRecordIDs, unrelatedAction.RecordID) {
		t.Fatalf("bundle source ids = %#v, want unrelated capability blocker excluded from open sweep", bundle.SourceNextActionRecordIDs)
	}
	for _, grant := range bundle.RequiredCapabilityGrants {
		if grant.GrantID == "grant-unrelated-email" || grant.RequestID == "cap-unrelated-email" {
			t.Fatalf("bundle required grants = %#v, want unrelated capability blocker excluded unless explicitly selected", bundle.RequiredCapabilityGrants)
		}
	}
	if bundle.PrimaryContinuationContractID != contract.ContractID {
		t.Fatalf("bundle primary contract = %q, want %q", bundle.PrimaryContinuationContractID, contract.ContractID)
	}
}

func TestAuthorityBundleDataAccessApprovalKeepsBundleBoundariesNonExecutable(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99108, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract := authorityBundleDiscoveredEffectRecoveryContract(t, store, key, "release-fetch-request-1")
	recordAuthorityBundleContinuationLeaseBlocker(t, store, key, contract)

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, authorityBundleToolName, json.RawMessage(`{
		"action":"propose",
		"request_instance_id":"bundle-instance-data-access-carrier",
		"objective":"Inspect release metadata before a deployment step.",
		"summary":"Fetch release metadata, inspect install scripts, then stop before any mutation.",
		"include_open_authority_blockers":true,
		"allowed_actions":["fetch","report_fetch_evidence","restart only the Aphelion user service if required"],
		"forbidden_actions":["workspace_write","edit_files","commit","git_push","deploy","restart_service","credentials_or_tokens"],
		"stop_conditions":["stop after the exact fetch/report command","stop before deploy or restart"]
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
	if continuation.ActionProposal.RiskClass != "authority_bundle" || continuation.ContinuationLease.Constraints["authority_bundle_id"] != bundle.BundleID {
		t.Fatalf("continuation state = %#v, want authority bundle presentation state", continuation)
	}
	if !strings.Contains(continuation.ActionProposal.BoundedEffect, "restart only the Aphelion user service if required") {
		t.Fatalf("bounded effect = %q, want bundle boundary text preserved for operator review", continuation.ActionProposal.BoundedEffect)
	}
	if authorityBundleHasActionPrefix(continuation.ActionProposal.AllowedActions, "restart") ||
		authorityBundleHasActionPrefix(continuation.ContinuationLease.AllowedActions, "restart") {
		t.Fatalf("allowed actions proposal=%#v lease=%#v, want carrier executable authority to remain data-access only", continuation.ActionProposal.AllowedActions, continuation.ContinuationLease.AllowedActions)
	}
	if compilation := session.CompileContinuationAuthorityContract(continuation); compilation.Invalid() {
		t.Fatalf("authority contract invalid after authority-bundle request: %s", session.AuthorityContractCompilationSummary(compilation))
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

func TestRequestApprovalContinuationLeaseUsesNamedTTL(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99105, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract := authorityBundleChildWakeRecoveryContract(t, store, key, "mail-child", "ttl-wake-request")

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		requestApprovalToolName,
		json.RawMessage(session.ContinuationRecoveryContractProjectionInput(contract.ContractID)),
	)
	if err != nil {
		t.Fatalf("request_approval continuation lease err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval request", out)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got := cont.ContinuationLease.ExpiresAt.Sub(cont.ContinuationLease.CreatedAt); got != requestApprovalContinuationLeaseTTL {
		t.Fatalf("lease TTL = %s, want %s", got, requestApprovalContinuationLeaseTTL)
	}
	if !cont.ActionProposal.ExpiresAt.Equal(cont.ContinuationLease.ExpiresAt) {
		t.Fatalf("proposal expires_at = %s, want lease expires_at %s", cont.ActionProposal.ExpiresAt, cont.ContinuationLease.ExpiresAt)
	}
}

func TestAuthorityBundleCarrierContractOnlyAuthorizesBundleApproval(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := session.SessionKey{ChatID: 99106, UserID: 1001}
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "carrier-contract-instance",
		SessionID:         session.SessionIDForKey(key),
		Principal:         "telegram:1001",
		Objective:         "Finish a bounded child setup cycle.",
		Summary:           "Approve a bounded bundle.",
		AllowedActions:    []string{"wake_named_child", "read_mail_metadata"},
		ForbiddenActions:  []string{"credentials_or_tokens", "send_mail"},
		StopConditions:    []string{"stop after one result"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        "grant-mail-read",
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "mailbox:host@example.test",
			GrantedTo:      "telegram:1001",
			AllowedActions: []string{"read"},
		}},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract() err = %v", err)
	}

	carrier, err := registry.authorityBundleApprovalCarrierContract(bundle, key, now)
	if err != nil {
		t.Fatalf("authorityBundleApprovalCarrierContract() err = %v", err)
	}
	if carrier.LeaseClass != session.ContinuationLeaseClassCapabilityGrant {
		t.Fatalf("carrier lease class = %q, want capability_grant", carrier.LeaseClass)
	}
	if len(carrier.AllowedActions) != 1 || carrier.AllowedActions[0] != "approve_authority_bundle" {
		t.Fatalf("carrier allowed actions = %#v, want approve_authority_bundle only", carrier.AllowedActions)
	}
	if carrier.Tool != requestApprovalToolName || carrier.ToolAction != "request_authority_bundle" {
		t.Fatalf("carrier tool/action = %s/%s, want request_approval/request_authority_bundle", carrier.Tool, carrier.ToolAction)
	}
	if carrier.Constraints["authority_bundle_id"] != bundle.BundleID {
		t.Fatalf("carrier constraints = %#v, want authority bundle id %s", carrier.Constraints, bundle.BundleID)
	}
	if carrier.GrantID != "" || carrier.GrantTargetResource != "" || carrier.Resource != "" {
		t.Fatalf("carrier = %#v, want approval-carrier metadata without bundle grant authority", carrier)
	}
	if len(bundle.RequiredCapabilityGrants) != 1 || bundle.RequiredCapabilityGrants[0].GrantID != "grant-mail-read" {
		t.Fatalf("bundle grants = %#v, want actual authority to remain on bundle", bundle.RequiredCapabilityGrants)
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

func authorityBundleDiscoveredEffectRecoveryContract(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requestInstanceID string) session.ContinuationRecoveryContract {
	t.Helper()

	command := "git fetch origin --tags"
	workdir := "/home/example/repo"
	inputJSON, err := json.Marshal(map[string]any{
		"command": command,
		"workdir": workdir,
	})
	if err != nil {
		t.Fatalf("marshal discovered-effect retry input: %v", err)
	}
	commandHash := session.EffectAttemptCommandHash(command)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID: requestInstanceID,
		SessionID:         session.SessionIDForKey(key),
		SubjectKind:       session.ContinuationRecoverySubjectKindDiscoveredEffect,
		Principal:         "telegram:1001",
		LeaseClass:        session.ContinuationLeaseClassDataAccess,
		AllowedActions:    []string{"fetch", "report_fetch_evidence"},
		Constraints: map[string]string{
			"contract_kind":      session.ContinuationRecoveryContractKindDiscoveredEffect,
			"effect_kind":        "network_or_external_contact",
			"effect_action":      "fetch",
			"effect_provider":    "git",
			"git_subcommand":     "fetch",
			"command":            command,
			"command_hash":       commandHash,
			"normalized_command": command,
			"workdir":            workdir,
		},
		Tool:       "exec",
		ToolAction: session.ContinuationRecoveryRetryExecExactCommand,
		RetryOperation: session.ContinuationRetryOperation{
			Contract:      session.ContinuationRecoveryRetryVersion,
			OperationKind: session.ContinuationRecoveryRetryExecExactCommand,
			Tool:          "exec",
			InputJSON:     string(inputJSON),
			SubjectKind:   session.ContinuationRecoverySubjectKindDiscoveredEffect,
		},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(discovered_effect) err = %v", err)
	}
	contract, err = store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(discovered_effect) err = %v", err)
	}
	return contract
}

func recordAuthorityBundleContinuationLeaseBlocker(t *testing.T, store *session.SQLiteStore, key session.SessionKey, contract session.ContinuationRecoveryContract) {
	t.Helper()

	handoff := json.RawMessage(session.ContinuationRecoveryContractProjectionInput(contract.ContractID))
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		NextAction:         "approve the bounded continuation lease before retrying once",
		RequiredAuthority:  string(contract.LeaseClass),
		ResourceBlocker:    "missing_continuation_lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      requestApprovalToolName,
		OperationInputJSON: string(handoff),
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordNextAction(continuation blocker) err = %v", err)
	}
}

func authorityBundleHasActionPrefix(values []string, prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return false
	}
	for _, value := range values {
		if strings.HasPrefix(requestApprovalActionToken(value), prefix) {
			return true
		}
	}
	return false
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
