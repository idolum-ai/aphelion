//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type stubDurableMemoryDelegationApprover struct {
	approved bool
	requests []DurableMemoryDelegationApprovalRequest
}

func (s *stubDurableMemoryDelegationApprover) ConfirmDurableMemoryDelegation(_ context.Context, req DurableMemoryDelegationApprovalRequest) (DurableMemoryDelegationApprovalDecision, error) {
	s.requests = append(s.requests, req)
	return DurableMemoryDelegationApprovalDecision{Approved: s.approved}, nil
}

func TestDurableAgentToolDefinitionIncludesMemoryDelegationSurface(t *testing.T) {
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
	if !strings.Contains(durableDefJSON, `"memory_review"`) || !strings.Contains(durableDefJSON, `"memory_delegate"`) {
		t.Fatalf("durable_agent definition missing memory delegation actions: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"memory_delegation"`) || !strings.Contains(durableDefJSON, `"candidate_ids"`) {
		t.Fatalf("durable_agent definition missing memory_delegation surface: %s", durableDefJSON)
	}
}

func TestDurableAgentToolMemoryReviewAndDelegateWithApproval(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	scope, err := registry.sandbox.Resolve(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("Resolve(admin scope) err = %v", err)
	}

	parentKnowledgePath, _, err := memstore.ResolveStorePath(scope.SharedMemoryRoot, "knowledge")
	if err != nil {
		t.Fatalf("ResolveStorePath(parent knowledge) err = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(parentKnowledgePath), 0o755); err != nil {
		t.Fatalf("MkdirAll(parent knowledge dir) err = %v", err)
	}
	parentKnowledge := strings.Join([]string{
		"# knowledge.md",
		"",
		"- Idolum should keep channel digests concise and pragmatic.",
		"- Escalate messages with explicit job opportunities quickly.",
	}, "\n")
	if err := os.WriteFile(parentKnowledgePath, []byte(parentKnowledge), 0o600); err != nil {
		t.Fatalf("WriteFile(parent knowledge) err = %v", err)
	}

	childWorkspace := filepath.Join(t.TempDir(), "child", "workspace")
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review an external child channel and surface important threads.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		PolicyVersion:     1,
		LocalStorageRoots: []string{childWorkspace, childMemory},
		WakeupMode:        "poll",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	reviewOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"memory_review","agent_id":"child-alpha","memory_delegation":{"limit":5}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(memory_review) err = %v", err)
	}
	if !strings.Contains(reviewOut, "action: durable-agent memory review") {
		t.Fatalf("memory_review output = %q, want memory review action", reviewOut)
	}
	candidateID := firstMemoryCandidateID(reviewOut)
	if candidateID == "" {
		t.Fatalf("memory_review output = %q, want candidate id", reviewOut)
	}

	approver := &stubDurableMemoryDelegationApprover{approved: true}
	registry.WithDurableMemoryDelegationApprover(approver)
	delegateInput := fmt.Sprintf(`{
		"action":"memory_delegate",
		"agent_id":"child-alpha",
		"memory_delegation":{
			"candidate_ids":["%s"],
			"reason":"Seed durable child context for channel triage."
		}
	}`, candidateID)
	delegateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(delegateInput),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(memory_delegate) err = %v", err)
	}
	if !strings.Contains(delegateOut, "action: durable-agent memory delegate") {
		t.Fatalf("memory_delegate output = %q, want memory delegation action", delegateOut)
	}
	if !strings.Contains(delegateOut, "approved: true") || !strings.Contains(delegateOut, "changed: true") {
		t.Fatalf("memory_delegate output = %q, want approved changed result", delegateOut)
	}
	if len(approver.requests) != 1 {
		t.Fatalf("approver requests = %#v, want one request", approver.requests)
	}
	if approver.requests[0].Agent.AgentID != "child-alpha" {
		t.Fatalf("approver agent_id = %q, want child-alpha", approver.requests[0].Agent.AgentID)
	}
	if len(approver.requests[0].Entries) == 0 {
		t.Fatalf("approver entries = %#v, want delegated entries", approver.requests[0].Entries)
	}

	childKnowledgePath, _, err := memstore.ResolveStorePath(childMemory, "knowledge")
	if err != nil {
		t.Fatalf("ResolveStorePath(child knowledge) err = %v", err)
	}
	raw, err := os.ReadFile(childKnowledgePath)
	if err != nil {
		t.Fatalf("ReadFile(child knowledge) err = %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "concise and pragmatic") {
		t.Fatalf("child knowledge = %q, want delegated memory content", got)
	}
	if !strings.Contains(got, "delegated_from_parent") {
		t.Fatalf("child knowledge = %q, want delegated provenance tag", got)
	}
}

func TestDurableAgentToolMemoryDelegateDeniedDoesNotWrite(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	childWorkspace := filepath.Join(t.TempDir(), "child", "workspace")
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review an external child channel and surface important threads.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		PolicyVersion:     1,
		LocalStorageRoots: []string{childWorkspace, childMemory},
		WakeupMode:        "poll",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	approver := &stubDurableMemoryDelegationApprover{approved: false}
	registry.WithDurableMemoryDelegationApprover(approver)
	delegateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"memory_delegate",
			"agent_id":"child-alpha",
			"memory_delegation":{
				"entries":[{"content":"Keep summaries concise.","target_store":"knowledge"}],
				"reason":"Attempted memory delegation."
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(memory_delegate denied) err = %v", err)
	}
	if !strings.Contains(delegateOut, "approved: false") || !strings.Contains(delegateOut, "changed: false") {
		t.Fatalf("memory_delegate denied output = %q, want denied unchanged result", delegateOut)
	}
	childKnowledgePath, _, err := memstore.ResolveStorePath(childMemory, "knowledge")
	if err != nil {
		t.Fatalf("ResolveStorePath(child knowledge) err = %v", err)
	}
	raw, readErr := os.ReadFile(childKnowledgePath)
	if readErr == nil && strings.Contains(string(raw), "Keep summaries concise.") {
		t.Fatalf("child knowledge = %q, do not want writes on denied delegation", string(raw))
	}
}

func firstMemoryCandidateID(output string) string {
	re := regexp.MustCompile(`candidate_id=([a-z_]+:[0-9]+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}
