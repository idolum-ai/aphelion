//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type stubDurableSnapshotRestoreApprover struct {
	approved bool
	requests []DurableSnapshotRestoreApprovalRequest
}

func (s *stubDurableSnapshotRestoreApprover) ConfirmDurableSnapshotRestore(_ context.Context, req DurableSnapshotRestoreApprovalRequest) (DurableSnapshotRestoreApprovalDecision, error) {
	s.requests = append(s.requests, req)
	return DurableSnapshotRestoreApprovalDecision{Approved: s.approved}, nil
}

func TestDurableAgentToolDefinitionIncludesSnapshotActions(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(newToolTestStore(t))
	var durableDefJSON string
	for _, def := range registry.Definitions() {
		if def.Name == "durable_agent" {
			durableDefJSON = string(def.Parameters)
			break
		}
	}
	if durableDefJSON == "" {
		t.Fatal("durable_agent definition missing")
	}
	if !strings.Contains(durableDefJSON, `"snapshot_create"`) || !strings.Contains(durableDefJSON, `"snapshot_restore"`) {
		t.Fatalf("durable_agent definition missing snapshot actions: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"snapshot"`) || !strings.Contains(durableDefJSON, `"snapshot_id"`) {
		t.Fatalf("durable_agent definition missing snapshot payload: %s", durableDefJSON)
	}
}

func TestDurableAgentToolSnapshotLifecycle(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	childWorkspace := filepath.Join(t.TempDir(), "child", "workspace")
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	if err := os.MkdirAll(filepath.Join(childMemory, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(child memory) err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-child",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help locally and escalate important work."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help locally and escalate important work.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/test-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{childWorkspace, childMemory},
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := os.MkdirAll(childWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(child workspace) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(childWorkspace, "task.txt"), []byte("before"), 0o600); err != nil {
		t.Fatalf("WriteFile(task before) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(childMemory, "memory", "knowledge.md"), []byte("- before"), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge before) err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   "idolum-child",
		StateJSON: `{"conversation":{"messages":[{"role":"child","text":"before"}]}}`,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState(before) err = %v", err)
	}

	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"snapshot_create","agent_id":"idolum-child","snapshot":{"reason":"before big change"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(snapshot_create) err = %v", err)
	}
	if !strings.Contains(createOut, "action: durable-agent snapshot create") {
		t.Fatalf("snapshot_create output = %q, want snapshot create action", createOut)
	}
	snapshotID := extractSnapshotID(createOut)
	if snapshotID == "" {
		t.Fatalf("snapshot_create output = %q, want snapshot_id", createOut)
	}

	if err := os.WriteFile(filepath.Join(childWorkspace, "task.txt"), []byte("after"), 0o600); err != nil {
		t.Fatalf("WriteFile(task after) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(childMemory, "memory", "knowledge.md"), []byte("- after"), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge after) err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   "idolum-child",
		StateJSON: `{"conversation":{"messages":[{"role":"child","text":"after"}]}}`,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState(after) err = %v", err)
	}

	approver := &stubDurableSnapshotRestoreApprover{approved: true}
	registry.WithDurableSnapshotRestoreApprover(approver)
	restoreOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"snapshot_restore","agent_id":"idolum-child","snapshot":{"snapshot_id":"`+snapshotID+`","reason":"rollback"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(snapshot_restore) err = %v", err)
	}
	if !strings.Contains(restoreOut, "approved: true") || !strings.Contains(restoreOut, "changed: true") {
		t.Fatalf("snapshot_restore output = %q, want approved and changed", restoreOut)
	}
	if len(approver.requests) != 1 {
		t.Fatalf("approver requests = %#v, want one request", approver.requests)
	}

	taskRaw, err := os.ReadFile(filepath.Join(childWorkspace, "task.txt"))
	if err != nil {
		t.Fatalf("ReadFile(task restored) err = %v", err)
	}
	if strings.TrimSpace(string(taskRaw)) != "before" {
		t.Fatalf("task content = %q, want restored before value", string(taskRaw))
	}
	knowledgeRaw, err := os.ReadFile(filepath.Join(childMemory, "memory", "knowledge.md"))
	if err != nil {
		t.Fatalf("ReadFile(knowledge restored) err = %v", err)
	}
	if !strings.Contains(string(knowledgeRaw), "before") {
		t.Fatalf("knowledge content = %q, want restored before value", string(knowledgeRaw))
	}
	state, err := store.DurableAgentState("idolum-child")
	if err != nil {
		t.Fatalf("DurableAgentState(restored) err = %v", err)
	}
	if !strings.Contains(state.StateJSON, `"before"`) {
		t.Fatalf("state.StateJSON = %q, want restored state", state.StateJSON)
	}
}

func TestDurableAgentToolSnapshotRestoreDeniedDoesNotChange(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	childWorkspace := filepath.Join(t.TempDir(), "child", "workspace")
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	if err := os.MkdirAll(filepath.Join(childMemory, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(child memory) err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-child",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help locally and escalate important work."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help locally and escalate important work.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/test-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{childWorkspace, childMemory},
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := os.MkdirAll(childWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(child workspace) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(childWorkspace, "task.txt"), []byte("before"), 0o600); err != nil {
		t.Fatalf("WriteFile(task before) err = %v", err)
	}
	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"snapshot_create","agent_id":"idolum-child","snapshot":{"reason":"before edit"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(snapshot_create) err = %v", err)
	}
	snapshotID := extractSnapshotID(createOut)
	if snapshotID == "" {
		t.Fatalf("snapshot_create output = %q, want snapshot_id", createOut)
	}
	if err := os.WriteFile(filepath.Join(childWorkspace, "task.txt"), []byte("after"), 0o600); err != nil {
		t.Fatalf("WriteFile(task after) err = %v", err)
	}

	registry.WithDurableSnapshotRestoreApprover(&stubDurableSnapshotRestoreApprover{approved: false})
	restoreOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"snapshot_restore","agent_id":"idolum-child","snapshot":{"snapshot_id":"`+snapshotID+`"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(snapshot_restore denied) err = %v", err)
	}
	if !strings.Contains(restoreOut, "approved: false") || !strings.Contains(restoreOut, "changed: false") {
		t.Fatalf("snapshot_restore denied output = %q, want denied unchanged result", restoreOut)
	}
	taskRaw, err := os.ReadFile(filepath.Join(childWorkspace, "task.txt"))
	if err != nil {
		t.Fatalf("ReadFile(task after denied) err = %v", err)
	}
	if strings.TrimSpace(string(taskRaw)) != "after" {
		t.Fatalf("task content = %q, want unchanged after value", string(taskRaw))
	}
}

func TestDurableAgentToolSnapshotRestoreRejectsInvalidIDBeforeApproval(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	childWorkspace := filepath.Join(t.TempDir(), "child", "workspace")
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-child",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help locally and escalate important work."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help locally and escalate important work.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/test-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{childWorkspace, childMemory},
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	approver := &stubDurableSnapshotRestoreApprover{approved: true}
	registry.WithDurableSnapshotRestoreApprover(approver)

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"snapshot_restore","agent_id":"idolum-child","snapshot":{"snapshot_id":"../crafted"}}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(snapshot_restore invalid id) err = nil, want validation error")
	}
	if len(approver.requests) != 0 {
		t.Fatalf("approver requests = %#v, want no approval before snapshot validation", approver.requests)
	}
}

func extractSnapshotID(output string) string {
	re := regexp.MustCompile(`(?m)^snapshot_id:\s*([^\s]+)\s*$`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}
