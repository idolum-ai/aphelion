//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"errors"
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

	surfaces := []struct {
		name     string
		producer string
		consumer string
	}{
		{"missing capability grant", "tool.materializeMissingGrantError", "capability_authority grant_set"},
		{"missing continuation lease", "tool.materializeMissingContinuationLeaseError", "request_approval request_continuation_lease"},
		{"rejected shell alternative", "tool.recordRejectedExecNextAction", "typed native tool or update_operation"},
		{"uncertain effect attempt", "session.UpsertEffectAttempt", "verification next action"},
		{"resource preflight blocker", "tool.recordNativeResourcePreflight", "resource repair next action"},
		{"durable child wake outcome", "runtime.CommitChildTaskOutcome", "post-outcome intent executor"},
	}
	for _, surface := range surfaces {
		if strings.TrimSpace(surface.name) == "" || strings.TrimSpace(surface.producer) == "" || strings.TrimSpace(surface.consumer) == "" {
			t.Fatalf("surface inventory contains incomplete row: %#v", surface)
		}
	}
}

var _ = core.ExecutionEventContinuationOffered
