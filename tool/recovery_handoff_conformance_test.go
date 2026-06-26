//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestRecoveryHandoffMissingContinuationLeaseActionsAreExecutable(t *testing.T) {
	t.Parallel()

	t.Run("child wake lease request", func(t *testing.T) {
		t.Parallel()

		registry, store := newDurableAgentToolRegistry(t)
		registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
		upsertDurableAgentWakeTestAgent(t, store)
		grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
		key := session.SessionKey{ChatID: 88101, UserID: 1001}

		_, err := registry.ExecuteForSessionPrincipal(
			context.Background(),
			principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
			key,
			"durable_agent",
			json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
		)
		if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") {
			t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want child_wake lease blocker", err)
		}
		action := singleOpenRecoveryAction(t, store, key, session.NextActionBlockedNeedsAuthority, "request_approval")
		out := executeRecoveryHandoffAction(t, registry, key, principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, action)
		if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
			t.Fatalf("request_approval output = %q, want approval request", out)
		}
		assertPendingLeaseShape(t, store, key, session.ContinuationLeaseClassChildWake, map[string]string{"agent_id": "child-alpha"}, durableAgentWakeOnceAction)
	})

	t.Run("native file data access lease request", func(t *testing.T) {
		t.Parallel()

		registry, store := newDurableAgentToolRegistry(t)
		workspace := t.TempDir()
		externalRoot := t.TempDir()
		target := filepath.Join(externalRoot, "runtime-bin")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
		if err := os.WriteFile(filepath.Join(target, "probe.txt"), []byte("child-local metadata\n"), 0o600); err != nil {
			t.Fatalf("write target file: %v", err)
		}
		actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
		key := session.SessionKey{ChatID: 88102, UserID: 1001}
		if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
			GrantID:        "capg-recovery-handoff-runtime-read",
			GrantedBy:      "telegram:1001",
			GrantedTo:      "telegram:1001",
			Kind:           session.CapabilityKindFileAccess,
			TargetResource: target,
			AllowedActions: []string{"read"},
			Status:         session.CapabilityGrantStatusActive,
		}); err != nil {
			t.Fatalf("UpsertCapabilityGrant(file_access) err = %v", err)
		}
		scope := sandbox.Scope{
			Principal:        actor,
			Profile:          sandbox.DefaultProfiles().Admin,
			GlobalRoot:       filepath.Join(workspace, "global"),
			SharedMemoryRoot: filepath.Join(workspace, "shared"),
			WorkingRoot:      workspace,
		}

		_, err := registry.executeWithScopeAndPrincipal(
			context.Background(),
			"list_dir",
			json.RawMessage(`{"path":"`+filepath.ToSlash(target)+`"}`),
			scope,
			actor,
			key,
		)
		if err == nil || !strings.Contains(err.Error(), "missing data_access continuation lease") {
			t.Fatalf("list_dir err = %v, want data_access lease blocker", err)
		}
		action := singleOpenRecoveryAction(t, store, key, session.NextActionBlockedNeedsAuthority, "request_approval")
		out := executeRecoveryHandoffAction(t, registry, key, actor, action)
		if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
			t.Fatalf("request_approval output = %q, want approval request", out)
		}
		assertPendingLeaseShape(t, store, key, session.ContinuationLeaseClassDataAccess, map[string]string{
			"grant_id":  "capg-recovery-handoff-runtime-read",
			"operation": "list_dir",
			"resource":  target,
		}, "read_approved_resource")
	})
}

func TestRecoveryHandoffMissingGrantActionIsExecutableAfterReview(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := session.SessionKey{ChatID: 88103, UserID: 1001}
	ctx := contextWithDurableAgentWakeAuthority(t, store, key, actor, "lease-recovery-handoff-child-wake", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want missing grant blocker", err)
	}
	action := singleOpenRecoveryAction(t, store, key, session.NextActionBlockedNeedsAuthority, "capability_authority")
	input := nextActionInputMapForRecovery(t, action)
	requestID, _ := input["request_id"].(string)
	if strings.TrimSpace(requestID) == "" {
		t.Fatalf("next action input = %#v, want request_id", input)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"capability_authority",
		json.RawMessage(`{"action":"request_review","request_id":"`+requestID+`","review_status":"approved","rationale":"operator approved exact recovery handoff"}`),
	); err != nil {
		t.Fatalf("request_review approval err = %v", err)
	}
	out := executeRecoveryHandoffAction(t, registry, key, actor, action)
	if !strings.Contains(out, "[CAPABILITY_GRANT]") || !strings.Contains(out, "status: active") {
		t.Fatalf("grant_set output = %q, want active grant", out)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after grant_set) err = %v", err)
	}
	for _, item := range open {
		if item.SubjectKind == "capability_request" && item.SubjectRef == requestID {
			t.Fatalf("open next actions = %#v, want capability blocker resolved by grant_set", open)
		}
	}
}

func TestRecoveryHandoffRejectedShellReadyActionIsExecutable(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("typed recovery handoff\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	store := newToolTestStore(t)
	key := session.SessionKey{ChatID: 88104, UserID: 1001}
	registry := NewRegistry(workspace, 2*time.Second).WithSessionStore(store)
	ctx := WithToolInvocationRef(context.Background(), ToolInvocationRef{TurnRunID: 88104, InvocationID: "recovery-handoff-shell"})
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	scope := sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace, Principal: actor}

	_, err := registry.executeWithScopeAndPrincipal(
		ctx,
		"exec",
		json.RawMessage(`{"command":"/bin/cat README.md"}`),
		scope,
		actor,
		key,
	)
	if !errors.Is(err, ErrExecRejectedBeforeDispatch) {
		t.Fatalf("exec err = %v, want pre-dispatch rejection", err)
	}
	action := singleOpenRecoveryAction(t, store, key, session.NextActionReadyToExecute, "read_file")
	out := executeRecoveryHandoffActionWithScope(t, registry, key, actor, scope, action)
	if !strings.Contains(out, "typed recovery handoff") {
		t.Fatalf("read_file handoff output = %q, want README content", out)
	}
}

func TestRecoveryHandoffCompilersProduceConsumerValidatedPayloads(t *testing.T) {
	t.Parallel()

	leaseReq := durableAgentWakeOnceLeaseRequirement(
		"child-alpha",
		session.CapabilityGrant{
			GrantID:        "grant-child-alpha",
			TargetResource: "durable_agent:child-alpha:wake_once",
		},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
	)
	leaseReq.RequestInstanceID = "test-compiler-child-wake-request"
	leaseContract, err := continuationRecoveryContractFromMissingLeaseRequirement(session.SessionKey{}, leaseReq, time.Now().UTC())
	if err != nil {
		t.Fatalf("continuationRecoveryContractFromMissingLeaseRequirement() err = %v", err)
	}
	leaseOp, err := compileContinuationLeaseRecoveryHandoff(leaseContract)
	if err != nil {
		t.Fatalf("compileContinuationLeaseRecoveryHandoff() err = %v", err)
	}
	if leaseOp.Tool != "request_approval" || leaseOp.Kind != "continuation_lease_request" {
		t.Fatalf("lease operation = %#v, want request_approval continuation lease request", leaseOp)
	}
	if err := ValidateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, leaseOp.Tool, leaseOp.InputJSON); err != nil {
		t.Fatalf("validate lease operation err = %v", err)
	}
	mutatedLease := strings.Replace(leaseOp.InputJSON, `"contract_id":"`+leaseContract.ContractID+`"`, `"contract_id":""`, 1)
	if err := ValidateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, leaseOp.Tool, mutatedLease); err == nil {
		t.Fatal("validate mutated lease operation err = nil, want missing contract rejection")
	}

	grantReq := normalizeMissingGrantRequirement(missingGrantRequirement{
		Kind:           session.CapabilityKindTool,
		TargetResource: "durable_agent:child-alpha:wake_once",
		GrantedTo:      "telegram:1001",
		AllowedActions: []string{"invoke"},
		Contract:       `{"bounded_effect":"wake child-alpha once"}`,
		Constraints:    `{"agent_id":"child-alpha"}`,
	})
	grantOp, err := compileCapabilityGrantRecoveryHandoff(session.CapabilityRequest{RequestID: grantReq.RequestID}, grantReq)
	if err != nil {
		t.Fatalf("compileCapabilityGrantRecoveryHandoff() err = %v", err)
	}
	if grantOp.Tool != "capability_authority" || grantOp.Kind != "capability_grant_review" {
		t.Fatalf("grant operation = %#v, want capability_authority grant review", grantOp)
	}
	if err := ValidateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, grantOp.Tool, grantOp.InputJSON); err != nil {
		t.Fatalf("validate grant operation err = %v", err)
	}
	mutatedGrant := strings.Replace(grantOp.InputJSON, `"request_id":"`+grantReq.RequestID+`"`, `"request_id":""`, 1)
	if err := ValidateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, grantOp.Tool, mutatedGrant); err == nil {
		t.Fatal("validate mutated grant operation err = nil, want missing request rejection")
	}

	if err := ValidateRecoveryHandoffToolInput(session.NextActionReadyToExecute, "update_operation", `{"merge":true}`); err == nil {
		t.Fatal("ready update_operation validation err = nil, want advisory-only rejection")
	}
	if err := ValidateRecoveryHandoffToolInput(session.NextActionReadyToExecute, "not_a_tool", `{"ok":true}`); err == nil {
		t.Fatal("ready unknown tool validation err = nil, want executable tool rejection")
	}
	for _, raw := range []string{
		`{}`,
		`{"unit":null}`,
		`{"unit":123}`,
		`{"unit":""}`,
		`{"unit":"   "}`,
	} {
		if err := ValidateRecoveryHandoffToolInput(session.NextActionReadyToExecute, "system_log_read", raw); err == nil {
			t.Fatalf("validate system_log_read input %s err = nil, want non-empty string unit rejection", raw)
		}
	}
	if err := ValidateRecoveryHandoffToolInput(session.NextActionReadyToExecute, "system_log_read", `{"unit":"aphelion.service"}`); err != nil {
		t.Fatalf("validate system_log_read valid unit err = %v", err)
	}
}

func TestRecoveryHandoffExecutableConsumersAreRegisteredTools(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(newToolTestStore(t))
	defs := registry.Definitions()
	registered := map[string]bool{}
	for _, def := range defs {
		registered[strings.TrimSpace(def.Name)] = true
	}
	for _, spec := range RecoveryTransitionSpecs() {
		if strings.TrimSpace(spec.Adapter) == "" {
			t.Fatalf("recovery transition spec = %#v, want owning adapter", spec)
		}
		if !registered[spec.OperationTool] {
			t.Fatalf("registered tool definitions = %#v, want recovery transition consumer %q for %#v", registered, spec.OperationTool, spec)
		}
	}
	if registered["durable_child_repair"] || registered["durable_child_continuation"] {
		t.Fatalf("registered tool definitions = %#v, do not want placeholder durable child recovery consumers", registered)
	}
}

func TestRecoveryTransitionSpecsValidateRepresentativeRecords(t *testing.T) {
	t.Parallel()

	for _, spec := range RecoveryTransitionSpecs() {
		raw := representativeRecoveryTransitionInput(t, spec)
		action := session.NextActionRecord{
			State:              spec.State,
			SubjectKind:        "recovery_transition",
			SubjectRef:         strings.Join([]string{spec.Adapter, spec.OperationTool, spec.OperationKind}, ":"),
			OperationKind:      spec.OperationKind,
			OperationTool:      spec.OperationTool,
			OperationInputJSON: raw,
		}
		if err := ValidateRecoveryTransitionRecord(action); err != nil {
			t.Fatalf("ValidateRecoveryTransitionRecord(%#v) err = %v input=%s", spec, err, raw)
		}
	}
}

func representativeRecoveryTransitionInput(t *testing.T, spec RecoveryTransitionSpec) string {
	t.Helper()

	switch spec.OperationTool {
	case "request_approval":
		return `{
			"action":"request_continuation_lease",
			"contract_id":"crc-spec-child-alpha-request",
			"recovery_contract":"aphelion.recovery_handoff.v1",
			"recovery_operation_kind":"continuation_lease_request"
		}`
	case "capability_authority":
		return `{
			"action":"grant_set",
			"request_id":"capability-request-spec",
			"kind":"generic_delegation",
			"target_resource":"durable_agent:child-alpha:wake_once",
			"principal":"telegram:1001",
			"allowed_actions":["invoke"],
			"contract":{"bounded_effect":"wake child-alpha once"},
			"constraints":{"agent_id":"child-alpha"},
			"grant_status":"active",
			"recovery_contract":"aphelion.recovery_handoff.v1",
			"recovery_operation_kind":"capability_grant_review",
			"retry_after_grant":true
		}`
	case "read_file":
		return `{"path":"README.md","recovery_contract":"aphelion.recovery_handoff.v1"}`
	case "list_dir":
		return `{"path":".","limit":50,"recovery_contract":"aphelion.recovery_handoff.v1"}`
	case "search":
		return `{"query":"Aphelion","path":".","limit":20,"recovery_contract":"aphelion.recovery_handoff.v1"}`
	case "system_log_read":
		return `{"unit":"aphelion.service","limit":50,"recovery_contract":"aphelion.recovery_handoff.v1"}`
	case "exec":
		if spec.OperationKind == "validation_command" {
			return `{"command":"go test ./tool","recovery_contract":"aphelion.recovery_handoff.v1"}`
		}
		return `{"command":"git status --short","recovery_contract":"aphelion.recovery_handoff.v1"}`
	case "update_operation":
		return `{"merge":true,"recovery_contract":"aphelion.recovery_handoff.v1","recovery_handoff":{"contract":"aphelion.recovery_handoff.v1","operation_kind":"` + spec.OperationKind + `","operation_tool":"update_operation"}}`
	default:
		t.Fatalf("no representative input for recovery transition spec %#v", spec)
		return ""
	}
}

func TestRequestContinuationLeaseApprovalIsReplaySafeAndBoundToGrant(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(newToolTestStore(t))
	key := session.SessionKey{ChatID: 88106, UserID: 1001}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "test-runtime-read-request-1",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassDataAccess, "", "capg-runtime-read", "list_dir", "list_dir", "/child/runtime-bin"),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassDataAccess,
		AllowedActions:      []string{"read_approved_resource"},
		Constraints:         map[string]string{"capability_kind": string(session.CapabilityKindFileAccess), "grant_id": "capg-runtime-read", "operation": "list_dir", "resource": "/child/runtime-bin", "target_resource": "/child/runtime-bin", "tool": "list_dir", "tool_action": "list_dir"},
		Tool:                "list_dir",
		ToolAction:          "list_dir",
		GrantID:             "capg-runtime-read",
		GrantTargetResource: "/child/runtime-bin",
		Resource:            "/child/runtime-bin",
		CreatedAt:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(data_access) err = %v", err)
	}
	contract, err = registry.store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(data_access) err = %v", err)
	}
	raw := json.RawMessage(session.ContinuationRecoveryContractProjectionInput(contract.ContractID))
	scope := sandbox.Scope{WorkingRoot: registry.workspace, SharedMemoryRoot: registry.workspace, Principal: actor}
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "request_approval", raw, scope, actor, key); err != nil {
		t.Fatalf("first request_approval err = %v", err)
	}
	first, err := registry.store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(first) err = %v", err)
	}
	if first.ContinuationLease.PlanHash == "" || len(first.ContinuationLease.RequiredCapabilityGrants) != 1 || len(first.ContinuationLease.CapabilityGrantIDs) != 1 {
		t.Fatalf("first continuation lease = %#v, want plan hash and exact grant binding", first.ContinuationLease)
	}
	if first.ContinuationLease.RequiredCapabilityGrants[0].GrantID != "capg-runtime-read" || first.ContinuationLease.RequiredCapabilityGrants[0].TargetResource != "/child/runtime-bin" {
		t.Fatalf("required grants = %#v, want exact file_access grant binding", first.ContinuationLease.RequiredCapabilityGrants)
	}
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "request_approval", raw, scope, actor, key); err != nil {
		t.Fatalf("replayed request_approval err = %v", err)
	}
	second, err := registry.store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(second) err = %v", err)
	}
	if second.DecisionID != first.DecisionID || second.ContinuationLease.ID != first.ContinuationLease.ID || second.ContinuationLease.PlanHash != first.ContinuationLease.PlanHash {
		t.Fatalf("replayed continuation = %#v, want same identity as %#v", second, first)
	}

	active := second
	active.Status = session.ContinuationStatusApproved
	active.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	if err := registry.store.UpdateContinuationState(key, active); err != nil {
		t.Fatalf("UpdateContinuationState(active) err = %v", err)
	}
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "request_approval", raw, scope, actor, key); err != nil {
		t.Fatalf("replay against active matching continuation err = %v", err)
	}
	afterActive, err := registry.store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(after active replay) err = %v", err)
	}
	if afterActive.ContinuationLease.ID != active.ContinuationLease.ID || afterActive.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("active replay continuation = %#v, want unchanged active lease %#v", afterActive, active)
	}
	activeOp, err := registry.store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState(after active replay) err = %v", err)
	}
	if activeOp.Status != session.OperationStatusActive || activeOp.Stage != "approval_active" || activeOp.Proposal.Status != session.ProposalStatusApproved {
		t.Fatalf("operation after active replay = %#v, want active/approved projection", activeOp)
	}

	consumed := afterActive
	consumed.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	consumed.ContinuationLease.RemainingTurns = 0
	consumed.RemainingTurns = 0
	if err := registry.store.UpdateContinuationState(key, consumed); err != nil {
		t.Fatalf("UpdateContinuationState(consumed) err = %v", err)
	}
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "request_approval", raw, scope, actor, key); err != nil {
		t.Fatalf("replay against consumed matching continuation err = %v", err)
	}
	consumedOp, err := registry.store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState(after consumed replay) err = %v", err)
	}
	if consumedOp.Status != session.OperationStatusCompleted || consumedOp.Stage != "approval_consumed" || consumedOp.Proposal.Status != session.ProposalStatusApproved {
		t.Fatalf("operation after consumed replay = %#v, want consumed/approved projection", consumedOp)
	}

	revoked := consumed
	revoked.Status = session.ContinuationStatusRevoked
	revoked.ActionProposal.Status = session.ProposalStatusDenied
	revoked.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
	if err := registry.store.UpdateContinuationState(key, revoked); err != nil {
		t.Fatalf("UpdateContinuationState(revoked) err = %v", err)
	}
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "request_approval", raw, scope, actor, key); err != nil {
		t.Fatalf("replay against revoked matching continuation err = %v", err)
	}
	revokedOp, err := registry.store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState(after revoked replay) err = %v", err)
	}
	if revokedOp.Status != session.OperationStatusBlocked || revokedOp.Stage != "approval_revoked" || revokedOp.Proposal.Status != session.ProposalStatusDenied {
		t.Fatalf("operation after revoked replay = %#v, want revoked/denied projection", revokedOp)
	}

	conflictKey := session.SessionKey{ChatID: 88107, UserID: 1001}
	if err := registry.store.UpdateContinuationState(conflictKey, session.ContinuationState{
		Status: session.ContinuationStatusPending,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-conflicting",
			Status:         session.ContinuationLeaseStatusPending,
			LeaseClass:     session.ContinuationLeaseClassDataAccess,
			AllowedActions: []string{"read_approved_resource"},
			PlanHash:       "sha256:conflicting",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState(conflict) err = %v", err)
	}
	conflictContract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "test-runtime-read-conflict-request-1",
		SessionID:           session.SessionIDForKey(conflictKey),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassDataAccess, "", "capg-runtime-read", "list_dir", "list_dir", "/child/runtime-bin"),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassDataAccess,
		AllowedActions:      []string{"read_approved_resource"},
		Constraints:         map[string]string{"capability_kind": string(session.CapabilityKindFileAccess), "grant_id": "capg-runtime-read", "operation": "list_dir", "resource": "/child/runtime-bin", "target_resource": "/child/runtime-bin", "tool": "list_dir", "tool_action": "list_dir"},
		Tool:                "list_dir",
		ToolAction:          "list_dir",
		GrantID:             "capg-runtime-read",
		GrantTargetResource: "/child/runtime-bin",
		Resource:            "/child/runtime-bin",
		CreatedAt:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(conflict) err = %v", err)
	}
	conflictContract, err = registry.store.UpsertContinuationRecoveryContract(conflictContract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(conflict) err = %v", err)
	}
	conflictRaw := json.RawMessage(session.ContinuationRecoveryContractProjectionInput(conflictContract.ContractID))
	if _, err := registry.executeWithScopeAndPrincipal(context.Background(), "request_approval", conflictRaw, scope, actor, conflictKey); err == nil || !strings.Contains(err.Error(), "conflicts with existing pending continuation") {
		t.Fatalf("conflicting request_approval err = %v, want pending continuation conflict", err)
	}
}

func TestRecoveryHandoffChildWakeSequenceMatchesLiveFailureOrder(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := session.SessionKey{ChatID: 88105, UserID: 1001}
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Run one no-content readiness wake.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	leaseCtx := contextWithDurableAgentWakeAuthority(t, store, key, actor, "lease-recovery-sequence-child-wake", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		leaseCtx,
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") {
		t.Fatalf("first wake err = %v, want missing grant", err)
	}
	grantAction := singleOpenRecoveryAction(t, store, key, session.NextActionBlockedNeedsAuthority, "capability_authority")
	if err := ValidateRecoveryHandoffToolInput(grantAction.State, grantAction.OperationTool, grantAction.OperationInputJSON); err != nil {
		t.Fatalf("validate grant action err = %v", err)
	}
	requestID := strings.TrimSpace(fmt.Sprint(nextActionInputMapForRecovery(t, grantAction)["request_id"]))
	if requestID == "" {
		t.Fatalf("grant action input = %s, want request_id", grantAction.OperationInputJSON)
	}
	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"capability_authority",
		json.RawMessage(`{"action":"request_review","request_id":"`+requestID+`","review_status":"approved","rationale":"operator approved exact wake grant"}`),
	); err != nil {
		t.Fatalf("request_review approval err = %v", err)
	}
	if out := executeRecoveryHandoffAction(t, registry, key, actor, grantAction); !strings.Contains(out, "[CAPABILITY_GRANT]") {
		t.Fatalf("grant handoff output = %q, want capability grant", out)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{}); err != nil {
		t.Fatalf("clear synthetic grant-check continuation state err = %v", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") {
		t.Fatalf("second wake err = %v, want missing child_wake lease", err)
	}
	leaseAction := singleOpenRecoveryAction(t, store, key, session.NextActionBlockedNeedsAuthority, "request_approval")
	if err := ValidateRecoveryHandoffToolInput(leaseAction.State, leaseAction.OperationTool, leaseAction.OperationInputJSON); err != nil {
		t.Fatalf("validate lease action err = %v", err)
	}
	if out := executeRecoveryHandoffAction(t, registry, key, actor, leaseAction); !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("lease handoff output = %q, want approval request", out)
	}
	assertPendingLeaseShape(t, store, key, session.ContinuationLeaseClassChildWake, map[string]string{"agent_id": "child-alpha"}, durableAgentWakeOnceAction)

	approvedCtx := contextWithDurableAgentWakeAuthority(t, store, key, actor, "lease-recovery-sequence-approved", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})
	out, err := registry.ExecuteForSessionPrincipal(
		approvedCtx,
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("approved wake err = %v", err)
	}
	if !strings.Contains(out, "wake_status: completed") || fmt.Sprint(runner.calls) != "[child-alpha]" {
		t.Fatalf("approved wake output=%q calls=%#v, want one completed child wake", out, runner.calls)
	}
}

func singleOpenRecoveryAction(t *testing.T, store *session.SQLiteStore, key session.SessionKey, state session.NextActionState, toolName string) session.NextActionRecord {
	t.Helper()

	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	var matches []session.NextActionRecord
	for _, action := range open {
		if action.State == state && strings.TrimSpace(action.OperationTool) == toolName {
			matches = append(matches, action)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("open next actions = %#v, want exactly one %s action for state %s", open, toolName, state)
	}
	if strings.TrimSpace(matches[0].OperationInputJSON) == "" {
		t.Fatalf("next action = %#v, want operation input JSON", matches[0])
	}
	return matches[0]
}

func executeRecoveryHandoffAction(t *testing.T, registry *Registry, key session.SessionKey, actor principal.Principal, action session.NextActionRecord) string {
	t.Helper()

	return executeRecoveryHandoffActionWithScope(t, registry, key, actor, sandbox.Scope{WorkingRoot: registry.workspace, SharedMemoryRoot: registry.workspace, Principal: actor}, action)
}

func executeRecoveryHandoffActionWithScope(t *testing.T, registry *Registry, key session.SessionKey, actor principal.Principal, scope sandbox.Scope, action session.NextActionRecord) string {
	t.Helper()

	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		action.OperationTool,
		json.RawMessage(action.OperationInputJSON),
		scope,
		actor,
		key,
	)
	if err != nil {
		t.Fatalf("execute handoff tool=%s input=%s err = %v", action.OperationTool, action.OperationInputJSON, err)
	}
	return out
}

func nextActionInputMapForRecovery(t *testing.T, action session.NextActionRecord) map[string]any {
	t.Helper()

	var input map[string]any
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &input); err != nil {
		t.Fatalf("unmarshal operation input %q: %v", action.OperationInputJSON, err)
	}
	return input
}

func assertPendingLeaseShape(t *testing.T, store *session.SQLiteStore, key session.SessionKey, class session.ContinuationLeaseClass, constraints map[string]string, allowedAction string) {
	t.Helper()

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending continuation lease", cont)
	}
	if cont.ContinuationLease.LeaseClass != class {
		t.Fatalf("lease class = %q, want %q", cont.ContinuationLease.LeaseClass, class)
	}
	if allowedAction != "" && !operationStringSliceContains(cont.ContinuationLease.AllowedActions, allowedAction) {
		t.Fatalf("allowed actions = %#v, want %q", cont.ContinuationLease.AllowedActions, allowedAction)
	}
	for key, want := range constraints {
		if got := strings.TrimSpace(cont.ContinuationLease.Constraints[key]); got != want {
			t.Fatalf("constraint %s = %q, want %q in %#v", key, got, want, cont.ContinuationLease.Constraints)
		}
	}
}

func TestRecoveryHandoffSurfaceInventoryDocumentsRepresentativeStops(t *testing.T) {
	t.Parallel()

	specs := RecoveryTransitionSpecs()
	if len(specs) < 8 {
		t.Fatalf("recovery transition specs = %#v, want representative graph coverage", specs)
	}
	byAdapter := map[string]int{}
	for _, spec := range specs {
		if strings.TrimSpace(spec.Adapter) == "" || strings.TrimSpace(spec.OperationKind) == "" || strings.TrimSpace(spec.OperationTool) == "" {
			t.Fatalf("recovery transition spec contains incomplete row: %#v", spec)
		}
		byAdapter[spec.Adapter]++
	}
	for _, adapter := range []string{"missing_continuation_lease", "missing_capability_grant", "typed_shell_alternative", "operator_rewrite"} {
		if byAdapter[adapter] == 0 {
			t.Fatalf("recovery transition adapters = %#v, want %q represented", byAdapter, adapter)
		}
	}
}

var _ = core.ExecutionEventContinuationOffered
