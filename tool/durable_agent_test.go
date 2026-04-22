//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDefinitionsIncludeDurableAgentToolWhenStoreConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	names := make([]string, 0, len(registry.Definitions()))
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "durable_agent") {
		t.Fatalf("definitions without store = %#v, do not want durable_agent", names)
	}

	store := newToolTestStore(t)
	registry = NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	names = names[:0]
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if !containsString(names, "durable_agent") {
		t.Fatalf("definitions with store = %#v, want durable_agent", names)
	}
}

func TestDurableAgentToolDefinitionIncludesPolicyPatchSurface(t *testing.T) {
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
	if !strings.Contains(durableDefJSON, `"policy_patch"`) {
		t.Fatalf("durable_agent definition missing policy_patch field: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"policy_overrides"`) {
		t.Fatalf("durable_agent definition missing policy_overrides field: %s", durableDefJSON)
	}
}

func TestDurableAgentToolDefinitionIncludesWizardSurface(t *testing.T) {
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
	if !strings.Contains(durableDefJSON, `"wizard_start"`) {
		t.Fatalf("durable_agent definition missing wizard action enum: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"wizard_answers"`) {
		t.Fatalf("durable_agent definition missing wizard_answers field: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"access_grant"`) || !strings.Contains(durableDefJSON, `"telegram_user_ids"`) {
		t.Fatalf("durable_agent definition missing access control surface: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"capacity_negotiate"`) || !strings.Contains(durableDefJSON, `"capacity_contract"`) {
		t.Fatalf("durable_agent definition missing capacity contract surface: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"conversation_show"`) || !strings.Contains(durableDefJSON, `"conversation_send"`) || !strings.Contains(durableDefJSON, `"message"`) {
		t.Fatalf("durable_agent definition missing conversation surface: %s", durableDefJSON)
	}
}

func TestDurableAgentToolAccessGrantRevoke(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "anthropic",
		APIKey:         "sk-parent-default",
		Model:          "claude-parent",
	})
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	grantOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"access_grant","agent_id":"family-group","telegram_user_ids":[2002,2001]}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(access_grant) err = %v", err)
	}
	if !strings.Contains(grantOut, "action: durable-agent access grant") || !strings.Contains(grantOut, "allowed_telegram_user_ids: 2001,2002") {
		t.Fatalf("grant output = %q, want access grant summary", grantOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"access_show","agent_id":"family-group"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(access_show) err = %v", err)
	}
	if !strings.Contains(showOut, "allowed_telegram_user_ids: 2001,2002") {
		t.Fatalf("show output = %q, want allowlist visibility", showOut)
	}

	revokeOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"access_revoke","agent_id":"family-group","telegram_user_id":2001}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(access_revoke) err = %v", err)
	}
	if !strings.Contains(revokeOut, "allowed_telegram_user_ids: 2002") {
		t.Fatalf("revoke output = %q, want narrowed allowlist", revokeOut)
	}

	agent, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if len(agent.AllowedTelegramUserIDs) != 1 || agent.AllowedTelegramUserIDs[0] != 2002 {
		t.Fatalf("AllowedTelegramUserIDs = %#v, want [2002]", agent.AllowedTelegramUserIDs)
	}
}

func TestDurableAgentToolConversationSendAndShow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-email",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox and surface important threads.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	sendOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"conversation_send","agent_id":"idolum-email","message":"Please flag recruiter threads aggressively and keep digest concise."}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(conversation_send) err = %v", err)
	}
	if !strings.Contains(sendOut, "action: durable-agent conversation send") {
		t.Fatalf("conversation_send output = %q, want conversation action", sendOut)
	}
	if !strings.Contains(sendOut, "pending_parent_messages: 1") {
		t.Fatalf("conversation_send output = %q, want pending parent count", sendOut)
	}
	if !strings.Contains(sendOut, "thread_state: awaiting_child_pickup") {
		t.Fatalf("conversation_send output = %q, want explicit thread state", sendOut)
	}
	if !strings.Contains(sendOut, "Please flag recruiter threads aggressively") {
		t.Fatalf("conversation_send output = %q, want echoed message", sendOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"conversation_show","agent_id":"idolum-email","history":5}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(conversation_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent conversation show") {
		t.Fatalf("conversation_show output = %q, want conversation show action", showOut)
	}
	if !strings.Contains(showOut, "pending_parent_messages: 1") {
		t.Fatalf("conversation_show output = %q, want pending parent count", showOut)
	}
	if !strings.Contains(showOut, "thread_state: awaiting_child_pickup") {
		t.Fatalf("conversation_show output = %q, want explicit thread state", showOut)
	}

	state, err := store.DurableAgentState("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) != 1 {
		t.Fatalf("conversation messages = %#v, want 1", continuity.Conversation)
	}
	if continuity.Conversation.Messages[0].Role != "parent" {
		t.Fatalf("conversation role = %q, want parent", continuity.Conversation.Messages[0].Role)
	}
	if continuity.Conversation.Messages[0].AcknowledgedAt.IsZero() != true {
		t.Fatalf("conversation acknowledged_at = %v, want zero", continuity.Conversation.Messages[0].AcknowledgedAt)
	}
}

func TestDurableAgentToolConversationShowIncludesRetryStateOnInferenceFailure(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-email",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-ant-test",
			Model:          "claude-sonnet-4-6",
			MaxTokens:      4096,
		},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent notes when inference is available.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Please summarize the latest intake.", time.Now().UTC().Add(-2*time.Minute))
	continuity = continuity.WithConversationMessage("child", "Inference backends are unavailable after retries and fallback. This turn did not complete. You can /stop to cancel current work and try again.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   "idolum-email",
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"conversation_show","agent_id":"idolum-email","history":6}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(conversation_show) err = %v", err)
	}
	if !strings.Contains(showOut, "thread_state: retrying_after_inference_failure") {
		t.Fatalf("conversation_show output = %q, want retrying thread state", showOut)
	}
	if !strings.Contains(showOut, "last_child_error: Inference backends are unavailable after retries and fallback.") {
		t.Fatalf("conversation_show output = %q, want surfaced child inference error", showOut)
	}
}

func TestDurableAgentToolCapacityContractFlow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-email",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox and surface important threads.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapCeiling: core.NormalizeDurableAgentBootstrapCeiling(core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:   []string{"read_channel", "bounded_review_artifact"},
			AllowedOutboundModes: []string{"read_only", "draft_only"},
		}),
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "poll",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"capacity_show","agent_id":"idolum-email"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(capacity_show) err = %v", err)
	}
	if !strings.Contains(showOut, "capacity_state: unattested") {
		t.Fatalf("capacity_show output = %q, want unattested state", showOut)
	}

	negotiateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"capacity_negotiate",
			"agent_id":"idolum-email",
			"capacity_contract":{
				"parent_proposal":"You are bounded to inbox triage and summary only.",
				"child_self_assessment":"I can triage and summarize threads, but I cannot send mail and I'm uncertain about OCR-heavy PDFs.",
				"can":["triage_inbox","summarize_thread"],
				"cannot":["send_mail"],
				"uncertain":["ocr_heavy_pdf"],
				"success_criteria":["important threads surfaced within 5m"],
				"evidence_signals":["review artifact includes surfaced_count"],
				"probe_checklist":["process one synthetic inbox sample"]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(capacity_negotiate) err = %v", err)
	}
	if !strings.Contains(negotiateOut, "action: durable-agent capacity negotiate") {
		t.Fatalf("capacity_negotiate output = %q, want negotiate action", negotiateOut)
	}
	if !strings.Contains(negotiateOut, "capacity_state: provisional") {
		t.Fatalf("capacity_negotiate output = %q, want provisional state", negotiateOut)
	}
	if !strings.Contains(negotiateOut, "can: triage_inbox,summarize_thread") {
		t.Fatalf("capacity_negotiate output = %q, want can list", negotiateOut)
	}

	state, err := store.DurableAgentState("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if continuity.CapabilityContract == nil {
		t.Fatalf("CapabilityContract = nil, want persisted contract")
	}
	if continuity.CapabilityContract.Status != "provisional" {
		t.Fatalf("CapabilityContract.Status = %q, want provisional", continuity.CapabilityContract.Status)
	}
	if continuity.CapabilityContract.LastNegotiatedAt.IsZero() {
		t.Fatal("CapabilityContract.LastNegotiatedAt is zero, want timestamp")
	}

	probeOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"capacity_probe",
			"agent_id":"idolum-email",
			"capacity_contract":{
				"probe_results":["synthetic sample triaged in 2m; surfaced two high-priority threads"]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(capacity_probe) err = %v", err)
	}
	if !strings.Contains(probeOut, "action: durable-agent capacity probe") {
		t.Fatalf("capacity_probe output = %q, want probe action", probeOut)
	}
	if !strings.Contains(probeOut, "probe_results: synthetic sample triaged in 2m; surfaced two high-priority threads") {
		t.Fatalf("capacity_probe output = %q, want probe results", probeOut)
	}

	attestOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"capacity_attest","agent_id":"idolum-email"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(capacity_attest) err = %v", err)
	}
	if !strings.Contains(attestOut, "action: durable-agent capacity attest") {
		t.Fatalf("capacity_attest output = %q, want attest action", attestOut)
	}
	if !strings.Contains(attestOut, "capacity_state: verified") {
		t.Fatalf("capacity_attest output = %q, want verified state", attestOut)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"policy_apply","agent_id":"idolum-email","autonomy":"local_drafts","reason":"policy adjustment requires fresh attestation"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_apply) err = %v", err)
	}
	staleOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"capacity_show","agent_id":"idolum-email"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(capacity_show after policy_apply) err = %v", err)
	}
	if !strings.Contains(staleOut, "capacity_state: stale") {
		t.Fatalf("capacity_show output = %q, want stale state after policy change", staleOut)
	}
}

func TestDurableAgentToolCapacityAttestRequiresProbeEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-email-no-probe",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox and surface important threads.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapCeiling: core.NormalizeDurableAgentBootstrapCeiling(core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:   []string{"read_channel", "bounded_review_artifact"},
			AllowedOutboundModes: []string{"read_only", "draft_only"},
		}),
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "poll",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"capacity_negotiate",
			"agent_id":"idolum-email-no-probe",
			"capacity_contract":{
				"parent_proposal":"You are bounded to inbox triage and summary only.",
				"child_self_assessment":"I can triage and summarize threads but cannot send mail.",
				"success_criteria":["important threads surfaced within 5m"],
				"evidence_signals":["review artifact includes surfaced_count"]
			}
		}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(capacity_negotiate) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"capacity_attest","agent_id":"idolum-email-no-probe"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(capacity_attest) err = nil, want probe evidence requirement")
	}
	if !strings.Contains(err.Error(), "probe_results") {
		t.Fatalf("err = %v, want probe_results requirement", err)
	}
}

func TestDurableAgentToolListAndPolicyShow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	listOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"list"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(list) err = %v", err)
	}
	if !strings.Contains(listOut, "[DURABLE_AGENTS]") || !strings.Contains(listOut, "family-group") {
		t.Fatalf("list output = %q, want durable agent summary", listOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"policy_show","agent_id":"family-group"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent policy show") || !strings.Contains(showOut, "outbound_mode: reply_with_policy_authorization") {
		t.Fatalf("policy show output = %q, want policy details", showOut)
	}
}

func TestDurableAgentToolPolicyApplyUsesReviewEventProvenance(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	reviewID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole: "durable_agent",
		SourceScope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             "family-group",
			DurableAgentID: "family-group",
		},
		TargetAdminChatID: 1001,
		TargetScope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   "1001",
		},
		Summary: "family-group requested tighter reply control",
		Status:  "pending",
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(fmt.Sprintf(`{"action":"policy_apply","agent_id":"family-group","review_event_id":%d,"outbound_mode":"read_only"}`, reviewID)),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_apply) err = %v", err)
	}
	if !strings.Contains(out, "changed: true") || !strings.Contains(out, fmt.Sprintf("source_review_event_id: %d", reviewID)) {
		t.Fatalf("policy apply output = %q, want changed policy with review provenance", out)
	}

	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("updated outbound_mode = %q, want read_only", updated.LivePolicy.OutboundMode)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated policy_version = %d, want 2", updated.PolicyVersion)
	}
	updates, err := store.DurableAgentPolicyUpdates(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("DurableAgentPolicyUpdates() err = %v", err)
	}
	if len(updates) != 1 || updates[0].SourceReviewEventID != reviewID {
		t.Fatalf("policy updates = %#v, want one update linked to review event %d", updates, reviewID)
	}
}

func TestDurableAgentToolPolicyApplyAcceptsConversationDerivedPolicyFields(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"policy_apply","agent_id":"family-group","autonomy":"review_before_reply","visibility":"parent_relay_only","shared_context":"isolated","reason":"ratified conversational policy"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_apply conversation fields) err = %v", err)
	}
	if !strings.Contains(out, "autonomy: review_before_reply") {
		t.Fatalf("policy apply output = %q, want conversational autonomy summary", out)
	}
	if !strings.Contains(out, "visibility: parent_relay_only") {
		t.Fatalf("policy apply output = %q, want conversational visibility summary", out)
	}
	if !strings.Contains(out, "shared_context: isolated") {
		t.Fatalf("policy apply output = %q, want conversational shared-context summary", out)
	}

	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "reply_with_parent_review" {
		t.Fatalf("updated outbound_mode = %q, want reply_with_parent_review", updated.LivePolicy.OutboundMode)
	}
	if updated.LivePolicy.PublicSurfaceMode != "explicit_parent_relay_only" {
		t.Fatalf("updated public_surface_mode = %q, want explicit_parent_relay_only", updated.LivePolicy.PublicSurfaceMode)
	}
	if updated.LivePolicy.SharedInferenceReuse != "disabled" {
		t.Fatalf("updated shared_inference_reuse = %q, want disabled", updated.LivePolicy.SharedInferenceReuse)
	}
}

func TestDurableAgentToolPolicyApplyAcceptsStructuredPolicyPatch(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"policy_apply",
			"agent_id":"family-group",
			"policy_patch":{
				"charter":"Observe the channel and escalate important items.",
				"autonomy":"observe_only",
				"visibility":"private",
				"shared_context":"public_only",
				"capabilities":["group_reply"],
				"drift_policy":"admin_review"
			},
			"policy_overrides":{
				"outbound_mode":"read_only"
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_apply structured patch) err = %v", err)
	}
	if !strings.Contains(out, "autonomy: observe_only") {
		t.Fatalf("policy apply output = %q, want structured autonomy summary", out)
	}
	if !strings.Contains(out, "visibility: private") {
		t.Fatalf("policy apply output = %q, want structured visibility summary", out)
	}
	if !strings.Contains(out, "shared_context: public_only") {
		t.Fatalf("policy apply output = %q, want structured shared-context summary", out)
	}

	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.Charter != "Observe the channel and escalate important items." {
		t.Fatalf("updated charter = %q, want structured patch charter", updated.LivePolicy.Charter)
	}
	if len(updated.LivePolicy.CapabilityEnvelope) != 1 || updated.LivePolicy.CapabilityEnvelope[0] != "group_reply" {
		t.Fatalf("updated capabilities = %#v, want [group_reply]", updated.LivePolicy.CapabilityEnvelope)
	}
	if updated.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("updated outbound_mode = %q, want read_only", updated.LivePolicy.OutboundMode)
	}
	if updated.LivePolicy.PublicSurfaceMode != "none" {
		t.Fatalf("updated public_surface_mode = %q, want none", updated.LivePolicy.PublicSurfaceMode)
	}
	if updated.LivePolicy.SharedInferenceReuse != "allowed" {
		t.Fatalf("updated shared_inference_reuse = %q, want allowed", updated.LivePolicy.SharedInferenceReuse)
	}
	if updated.LivePolicy.SharedInferenceReuseScope != "public_prefix_only" {
		t.Fatalf("updated shared_inference_reuse_scope = %q, want public_prefix_only", updated.LivePolicy.SharedInferenceReuseScope)
	}
}

func TestDurableAgentToolPolicyApplyResolvesConversationStyleAgentReference(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"policy_apply","agent_id":"Family Group durable agent","autonomy":"review_before_reply"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_apply conversational reference) err = %v", err)
	}
	if !strings.Contains(out, "agent_id: family-group") {
		t.Fatalf("policy apply output = %q, want resolved canonical agent id", out)
	}

	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "reply_with_parent_review" {
		t.Fatalf("updated outbound_mode = %q, want reply_with_parent_review", updated.LivePolicy.OutboundMode)
	}
}

func TestDurableAgentToolPolicyApplyUnknownAgentListsAvailableAgents(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"policy_apply","agent_id":"missing-agent","autonomy":"observe_only"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(policy_apply missing agent) err = nil, want helpful not-found error")
	}
	if !strings.Contains(err.Error(), "available agent_ids: family-group") {
		t.Fatalf("err = %v, want available agent list", err)
	}
}

func TestDurableAgentToolEnrollmentShowAndUpdate(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "remote-child",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "remote_host",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Watch the host and escalate anomalies.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
		BootstrapCeiling: core.NormalizeDurableAgentBootstrapCeiling(core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:   []string{"bounded_review_artifact"},
			AllowedOutboundModes: []string{"read_only"},
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "child-key",
			Model:          "claude-test",
		},
		ControlPlaneSecret: "secret-v1",
		PolicyVersion:      1,
		LocalStorageRoots:  []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:      "default",
		WakeupMode:         "manual",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://parent.example.test/control",
		KeyFingerprint:   "keyfp-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
		EnrolledAt:       time.Unix(1710000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"enrollment_show","agent_id":"remote-child"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(enrollment_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent enrollment") || !strings.Contains(showOut, "status: active") {
		t.Fatalf("enrollment show output = %q, want enrollment details", showOut)
	}

	updateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"enrollment_update","agent_id":"remote-child","operation":"rotate_secret","secret":"secret-v2"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(enrollment_update) err = %v", err)
	}
	if !strings.Contains(updateOut, "action: durable-agent enrollment") || !strings.Contains(updateOut, "status: active") {
		t.Fatalf("enrollment update output = %q, want enrollment update summary", updateOut)
	}

	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.ControlPlaneSecret != "secret-v2" {
		t.Fatalf("updated control plane secret = %q, want secret-v2", updated.ControlPlaneSecret)
	}
}

func TestDurableAgentToolEnrollmentShowMissingIsExplicit(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"enrollment_show","agent_id":"family-group"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(enrollment_show missing enrollment) err = nil, want explicit missing-enrollment error")
	}
	if !strings.Contains(err.Error(), "has no remote enrollment") {
		t.Fatalf("err = %v, want explicit no-remote-enrollment guidance", err)
	}
}

func TestDurableAgentToolCreateAndActivateEmailDraft(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)

	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"create",
			"agent_id":"idolum-email",
			"channel_kind":"email",
			"charter":"Review the inbox, surface important threads, summarize PDFs, and never send mail.",
			"autonomy":"observe_only",
			"capabilities":["read_channel","bounded_review_artifact","summarize_pdf"],
			"wakeup_mode":"poll",
			"secret_scopes":["gogcli"],
			"channel_config":{
				"email":{
					"address":"idolum@example.com",
					"account":"idolum@example.com",
					"adapter":"gog_cli",
					"query":"label:inbox newer_than:7d",
					"poll_interval":"5m",
					"summarize_pdfs":true,
					"synthesis_cadence":"4h",
					"surface_rules":["job opportunity","external inquiry"],
					"never_retain":["oauth_token","password"]
				}
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(create email draft) err = %v", err)
	}
	if !strings.Contains(createOut, "action: durable-agent create") || !strings.Contains(createOut, "status: draft") {
		t.Fatalf("create output = %q, want durable-agent create draft summary", createOut)
	}
	if !strings.Contains(createOut, "channel_kind: email") || !strings.Contains(createOut, "channel_profile: inbox") {
		t.Fatalf("create output = %q, want internal channel kind with inbox profile alias", createOut)
	}
	if !strings.Contains(createOut, "email_address: idolum@example.com") {
		t.Fatalf("create output = %q, want email address summary", createOut)
	}
	if !strings.Contains(createOut, "channel_address: idolum@example.com") {
		t.Fatalf("create output = %q, want channel address summary alias", createOut)
	}

	draft, err := store.DurableAgent("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgent(draft) err = %v", err)
	}
	if draft.Status != "draft" {
		t.Fatalf("draft status = %q, want draft", draft.Status)
	}
	if draft.ReviewTargetChatID != 1001 {
		t.Fatalf("ReviewTargetChatID = %d, want 1001", draft.ReviewTargetChatID)
	}
	if draft.ParentScopeKind != string(session.ScopeKindTelegramDM) || draft.ParentScopeID != "1001" {
		t.Fatalf("parent scope = kind:%q id:%q, want telegram_dm/1001", draft.ParentScopeKind, draft.ParentScopeID)
	}
	if draft.ChannelConfig.Email == nil || draft.ChannelConfig.Email.Adapter != "gog_cli" {
		t.Fatalf("ChannelConfig.Email = %#v, want gog_cli email config", draft.ChannelConfig.Email)
	}
	if draft.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("LivePolicy.OutboundMode = %q, want read_only", draft.LivePolicy.OutboundMode)
	}

	activateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"activate","agent_id":"idolum-email"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(activate email draft) err = %v", err)
	}
	if !strings.Contains(activateOut, "action: durable-agent activate") || !strings.Contains(activateOut, "status: active") {
		t.Fatalf("activate output = %q, want activation summary", activateOut)
	}

	activated, err := store.DurableAgent("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgent(activated) err = %v", err)
	}
	if activated.Status != "active" {
		t.Fatalf("activated status = %q, want active", activated.Status)
	}
}

func TestDurableAgentToolCreateInheritsBootstrapFromParentDefault(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "anthropic",
		APIKey:         "sk-parent-default",
		Model:          "claude-sonnet-4-6",
		MaxTokens:      2048,
	})

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"create",
			"agent_id":"idolum-inherit-create",
			"channel_kind":"telegram_dm",
			"charter":"Handle delegated DM triage."
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(create with inherited bootstrap) err = %v", err)
	}

	created, err := store.DurableAgent("idolum-inherit-create")
	if err != nil {
		t.Fatalf("DurableAgent(created) err = %v", err)
	}
	if got := core.NormalizeNodeLLMBootstrap(created.BootstrapLLM); !got.Configured() {
		t.Fatalf("created BootstrapLLM = %#v, want inherited configured bootstrap", created.BootstrapLLM)
	}
	if created.BootstrapLLM.Backend != "native" || created.BootstrapLLM.NativeProvider != "anthropic" {
		t.Fatalf("created BootstrapLLM = %#v, want inherited native anthropic bootstrap", created.BootstrapLLM)
	}
	if created.BootstrapLLM.APIKey != "sk-parent-default" {
		t.Fatalf("created BootstrapLLM.APIKey = %q, want inherited sk-parent-default", created.BootstrapLLM.APIKey)
	}
}

func TestDurableAgentToolActivateBackfillsBootstrapFromParentDefault(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "anthropic",
		APIKey:         "sk-parent-default",
		Model:          "claude-sonnet-4-6",
		MaxTokens:      2048,
	})

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-inherit-activate",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_dm",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_dm", core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage.")),
		WakeupMode:         "telegram_update",
		Status:             "draft",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent(draft without bootstrap) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"activate","agent_id":"idolum-inherit-activate"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(activate with inherited bootstrap) err = %v", err)
	}

	activated, err := store.DurableAgent("idolum-inherit-activate")
	if err != nil {
		t.Fatalf("DurableAgent(activated) err = %v", err)
	}
	if activated.Status != "active" {
		t.Fatalf("activated status = %q, want active", activated.Status)
	}
	if got := core.NormalizeNodeLLMBootstrap(activated.BootstrapLLM); !got.Configured() {
		t.Fatalf("activated BootstrapLLM = %#v, want inherited configured bootstrap", activated.BootstrapLLM)
	}
	if activated.BootstrapLLM.APIKey != "sk-parent-default" {
		t.Fatalf("activated BootstrapLLM.APIKey = %q, want inherited sk-parent-default", activated.BootstrapLLM.APIKey)
	}
}

func TestDurableAgentToolCreateSupportsInboxAliasChannelKindAndConfig(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)

	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"create",
			"agent_id":"idolum-inbox-alias",
			"channel_kind":"inbox",
			"wakeup_mode":"poll",
			"channel_config":{
				"inbox":{
					"address":"idolum@example.com",
					"adapter":"gog_cli"
				}
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(create inbox alias) err = %v", err)
	}
	if !strings.Contains(createOut, "channel_kind: email") || !strings.Contains(createOut, "channel_profile: inbox") {
		t.Fatalf("create output = %q, want canonical channel kind with inbox alias profile", createOut)
	}

	agent, err := store.DurableAgent("idolum-inbox-alias")
	if err != nil {
		t.Fatalf("DurableAgent(idolum-inbox-alias) err = %v", err)
	}
	if agent.ChannelKind != "email" {
		t.Fatalf("ChannelKind = %q, want canonical email", agent.ChannelKind)
	}
	if agent.ChannelConfig.Email == nil {
		t.Fatal("ChannelConfig.Email = nil, want normalized inbox/email config")
	}
	if agent.ChannelConfig.Email.Address != "idolum@example.com" {
		t.Fatalf("ChannelConfig.Email.Address = %q, want idolum@example.com", agent.ChannelConfig.Email.Address)
	}
	if agent.ChannelConfig.Email.Adapter != "gog_cli" {
		t.Fatalf("ChannelConfig.Email.Adapter = %q, want gog_cli", agent.ChannelConfig.Email.Adapter)
	}
}

func TestDurableAgentToolEmailWizardHappyPath(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "anthropic",
		APIKey:         "sk-parent-default",
		Model:          "claude-parent",
	})

	startOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_start","agent_id":"idolum-email","channel_kind":"email"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_start) err = %v", err)
	}
	if !strings.Contains(startOut, "action: durable-agent wizard show") || !strings.Contains(startOut, "wizard_status: in_progress") {
		t.Fatalf("wizard_start output = %q, want in-progress wizard summary", startOut)
	}
	if !strings.Contains(startOut, "current_step: address") {
		t.Fatalf("wizard_start output = %q, want first address step", startOut)
	}

	answerOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"wizard_answer",
			"agent_id":"idolum-email",
			"wizard_answers":{
				"address":"idolum@example.com",
				"account":"idolum@example.com",
				"adapter":"gog_cli",
				"bootstrap_profile":"child_custom",
				"bootstrap_model":"claude-sonnet-4-6",
				"query":"label:inbox newer_than:7d",
				"charter":"Review the inbox, surface important threads, summarize PDFs, and never send mail.",
				"autonomy":"observe_only",
				"wakeup_mode":"poll_or_push",
				"poll_interval":"5m",
				"surface_rules":["job opportunity","external inquiry"],
				"summarize_pdfs":true,
				"synthesis_cadence":"4h",
				"capabilities":["read_channel","bounded_review_artifact","summarize_pdf"],
				"never_retain":["oauth_token","password"],
				"drift_policy":"admin_review"
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_answer) err = %v", err)
	}
	if !strings.Contains(answerOut, "wizard_status: ready") {
		t.Fatalf("wizard_answer output = %q, want ready wizard status", answerOut)
	}
	if !strings.Contains(answerOut, "bootstrap_profile: child_custom") {
		t.Fatalf("wizard_answer output = %q, want child bootstrap profile surfaced", answerOut)
	}
	if !strings.Contains(answerOut, "bootstrap_model: claude-sonnet-4-6") {
		t.Fatalf("wizard_answer output = %q, want child bootstrap model surfaced", answerOut)
	}

	finalizeOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"idolum-email"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_finalize) err = %v", err)
	}
	if !strings.Contains(finalizeOut, "action: durable-agent wizard finalize") {
		t.Fatalf("wizard_finalize output = %q, want wizard finalize action", finalizeOut)
	}
	if !strings.Contains(finalizeOut, "status: draft") {
		t.Fatalf("wizard_finalize output = %q, want draft status", finalizeOut)
	}

	agent, err := store.DurableAgent("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgent(idolum-email) err = %v", err)
	}
	if agent.Status != "draft" {
		t.Fatalf("agent status = %q, want draft", agent.Status)
	}
	if agent.WakeupMode != "poll_or_push" {
		t.Fatalf("agent wakeup_mode = %q, want poll_or_push", agent.WakeupMode)
	}
	if agent.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("agent outbound_mode = %q, want read_only", agent.LivePolicy.OutboundMode)
	}
	if agent.BootstrapLLM.Model != "claude-sonnet-4-6" {
		t.Fatalf("agent bootstrap model = %q, want claude-sonnet-4-6", agent.BootstrapLLM.Model)
	}
	if agent.ChannelConfig.Email == nil {
		t.Fatal("agent channel_config.email = nil, want configured email child")
	}
	if agent.ChannelConfig.Email.SynthesisCadence != "4h" {
		t.Fatalf("email synthesis_cadence = %q, want 4h", agent.ChannelConfig.Email.SynthesisCadence)
	}
	if len(agent.ChannelConfig.Email.NeverRetain) != 2 || agent.ChannelConfig.Email.NeverRetain[0] != "oauth_token" {
		t.Fatalf("email never_retain = %#v, want oauth_token/password", agent.ChannelConfig.Email.NeverRetain)
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if continuity.SetupWizard == nil {
		t.Fatal("continuity setup_wizard = nil, want finalized wizard state")
	}
	if continuity.SetupWizard.Status != "finalized" {
		t.Fatalf("continuity setup_wizard status = %q, want finalized", continuity.SetupWizard.Status)
	}
}

func TestDurableAgentToolEmailWizardFinalizeRequiresCompleteAnswers(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_start","agent_id":"idolum-email","channel_kind":"email"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_start) err = %v", err)
	}
	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_answer","agent_id":"idolum-email","wizard_answers":{"address":"idolum@example.com","adapter":"gog_cli"}}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_answer partial) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"idolum-email"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(wizard_finalize partial) err = nil, want missing-answer error")
	}
	if !strings.Contains(err.Error(), "missing wizard answers") {
		t.Fatalf("err = %v, want missing wizard answers guidance", err)
	}
}

func TestDurableAgentToolEmailWizardChildCustomRequiresBootstrapModel(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "anthropic",
		APIKey:         "sk-parent-default",
		Model:          "claude-parent",
	})

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_start","agent_id":"idolum-email","channel_kind":"email"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_start) err = %v", err)
	}

	answerOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"wizard_answer",
			"agent_id":"idolum-email",
			"wizard_answers":{
				"address":"idolum@example.com",
				"adapter":"gog_cli",
				"bootstrap_profile":"child_custom",
				"charter":"Read-only inbox child.",
				"autonomy":"observe_only",
				"wakeup_mode":"poll",
				"poll_interval":"5m",
				"surface_rules":["urgent"],
				"summarize_pdfs":true,
				"synthesis_cadence":"4h",
				"capabilities":["read_channel","bounded_review_artifact","summarize_pdf"],
				"never_retain":["secrets"],
				"drift_policy":"admin_review"
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_answer child_custom without model) err = %v", err)
	}
	if !strings.Contains(answerOut, "current_step: bootstrap_model") {
		t.Fatalf("wizard_answer output = %q, want bootstrap_model step", answerOut)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"idolum-email"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(wizard_finalize without bootstrap_model) err = nil, want missing-answer error")
	}
	if !strings.Contains(err.Error(), "bootstrap_model") {
		t.Fatalf("err = %v, want missing bootstrap_model guidance", err)
	}
}

func TestDurableAgentToolEmailWizardBootstrapInheritanceAndCustomModel(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "anthropic",
		APIKey:         "sk-parent-default",
		Model:          "claude-parent",
	})

	startOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_start","agent_id":"idolum-email","channel_kind":"email"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_start) err = %v", err)
	}
	if !strings.Contains(startOut, "bootstrap_profile: inherit_parent") {
		t.Fatalf("wizard_start output = %q, want inherited bootstrap profile", startOut)
	}
	if !strings.Contains(startOut, "bootstrap_model: claude-parent") {
		t.Fatalf("wizard_start output = %q, want inherited bootstrap model surfaced", startOut)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"wizard_answer",
			"agent_id":"idolum-email",
			"wizard_answers":{
				"address":"idolum@example.com",
				"adapter":"gog_cli",
				"bootstrap_profile":"child_custom",
				"bootstrap_model":"claude-custom-child",
				"charter":"Read-only inbox child.",
				"autonomy":"observe_only",
				"wakeup_mode":"poll",
				"poll_interval":"5m",
				"surface_rules":["urgent"],
				"summarize_pdfs":true,
				"synthesis_cadence":"4h",
				"capabilities":["read_channel","bounded_review_artifact","summarize_pdf"],
				"never_retain":["secrets"],
				"drift_policy":"admin_review"
			}
		}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_answer custom bootstrap) err = %v", err)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"idolum-email"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_finalize custom bootstrap) err = %v", err)
	}

	agent, err := store.DurableAgent("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgent(idolum-email) err = %v", err)
	}
	if agent.BootstrapLLM.Model != "claude-custom-child" {
		t.Fatalf("agent bootstrap model = %q, want claude-custom-child", agent.BootstrapLLM.Model)
	}
	if agent.BootstrapLLM.NativeProvider != "anthropic" {
		t.Fatalf("agent bootstrap native_provider = %q, want anthropic", agent.BootstrapLLM.NativeProvider)
	}
	if agent.BootstrapLLM.APIKey != "sk-parent-default" {
		t.Fatalf("agent bootstrap api_key = %q, want inherited sk-parent-default", agent.BootstrapLLM.APIKey)
	}
}

func TestDurableAgentToolEmailWizardCodexChildCustomDoesNotRequireBootstrapModel(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:         "codex",
		CodexAuthSource: "codex_cli",
		CodexHome:       "/tmp/codex-home",
	})

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_start","agent_id":"idolum-email","channel_kind":"email"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_start) err = %v", err)
	}

	answerOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"wizard_answer",
			"agent_id":"idolum-email",
			"wizard_answers":{
				"address":"idolum@example.com",
				"adapter":"gog_cli",
				"bootstrap_profile":"child_custom",
				"charter":"Read-only inbox child.",
				"autonomy":"observe_only",
				"wakeup_mode":"poll",
				"poll_interval":"5m",
				"surface_rules":["urgent"],
				"summarize_pdfs":true,
				"synthesis_cadence":"4h",
				"capabilities":["read_channel","bounded_review_artifact","summarize_pdf"],
				"never_retain":["secrets"],
				"drift_policy":"admin_review"
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_answer codex child_custom) err = %v", err)
	}
	if !strings.Contains(answerOut, "wizard_status: ready") {
		t.Fatalf("wizard_answer output = %q, want ready status without bootstrap_model requirement for codex", answerOut)
	}
	if strings.Contains(answerOut, "current_step: bootstrap_model") {
		t.Fatalf("wizard_answer output = %q, do not want bootstrap_model step for codex", answerOut)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"idolum-email"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_finalize codex child_custom) err = %v", err)
	}

	agent, err := store.DurableAgent("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgent(idolum-email) err = %v", err)
	}
	if agent.BootstrapLLM.Backend != "codex" {
		t.Fatalf("agent bootstrap backend = %q, want codex", agent.BootstrapLLM.Backend)
	}
	if agent.BootstrapLLM.CodexHome != "/tmp/codex-home" {
		t.Fatalf("agent bootstrap codex_home = %q, want /tmp/codex-home", agent.BootstrapLLM.CodexHome)
	}
}

func TestDurableAgentToolApprovedUserIsDenied(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		session.SessionKey{ChatID: 42, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "42"}},
		"durable_agent",
		json.RawMessage(`{"action":"list"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(durable_agent) err = nil, want admin-only denial")
	}
	if !strings.Contains(err.Error(), "admin-only") {
		t.Fatalf("err = %v, want admin-only denial", err)
	}
}

func newToolTestStore(t *testing.T) *session.SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func newDurableAgentToolRegistry(t *testing.T) (*Registry, *session.SQLiteStore) {
	t.Helper()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	store := newToolTestStore(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunner(t, registry)
	return registry, store
}

func adminSessionKey() session.SessionKey {
	return session.SessionKey{
		ChatID: 1001,
		UserID: 0,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"},
	}
}
