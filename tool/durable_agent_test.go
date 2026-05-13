//go:build linux

package tool

import (
	"context"
	"encoding/json"
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
	if !strings.Contains(durableDefJSON, `"mode"`) || !strings.Contains(durableDefJSON, `"sketch"`) {
		t.Fatalf("durable_agent definition missing lightweight mode surface: %s", durableDefJSON)
	}
}

func TestToolDefinitionsAvoidProviderVisibleProjectName(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	for _, def := range registry.Definitions() {
		raw := def.Description + "\n" + string(def.Parameters)
		if strings.Contains(raw, "Aphelion repo") {
			t.Fatalf("tool definition %s leaks project-name repo phrasing: %s", def.Name, raw)
		}
	}
}

func TestDurableAgentCreateRejectsPathLikeAgentID(t *testing.T) {
	registry, _ := newDurableAgentToolRegistry(t)

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"create","agent_id":"../escape","channel_kind":"external_channel"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(create) err = nil, want invalid agent id error")
	}
	if !strings.Contains(err.Error(), "path separators") {
		t.Fatalf("ExecuteForSessionPrincipal(create) err = %v, want path separator context", err)
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

func TestDurableAgentToolDelegationRequestAndReport(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-child",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "200",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Help family members while escalating purchases and account access.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-test",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	requestOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"delegation_request",
			"agent_id":"family-child",
			"delegation_request":{
				"request_id":"cap-family-amazon",
				"kind":"purchase",
				"target_resource":"amazon",
				"requested_for":"family-child",
				"purpose":"order approved school supplies",
				"risk_class":"spend",
				"contract":{"allowed":"school supplies only"},
				"constraints":{"max_usd":50},
				"questions":["May I place this order?"],
				"metadata":{"cart_id":"cart-1"}
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(delegation_request) err = %v", err)
	}
	if !strings.Contains(requestOut, "action: durable-agent delegation request") ||
		!strings.Contains(requestOut, "canonical_surface: capability_request") ||
		!strings.Contains(requestOut, "agreement_surface: durable_child_agreement") ||
		!strings.Contains(requestOut, "agreement_id: agreement-cap-family-amazon") ||
		!strings.Contains(requestOut, "request_id: cap-family-amazon") ||
		!strings.Contains(requestOut, "review_status: proposed") {
		t.Fatalf("delegation_request output = %q, want canonical capability request and agreement summary", requestOut)
	}

	agreement, ok, err := store.DurableChildAgreement("agreement-cap-family-amazon")
	if err != nil {
		t.Fatalf("DurableChildAgreement() err = %v", err)
	}
	if !ok {
		t.Fatal("DurableChildAgreement(agreement-cap-family-amazon) ok=false, want stored agreement")
	}
	if agreement.AgentID != "family-child" || agreement.SourceRequestID != "cap-family-amazon" || agreement.Status != session.DurableChildAgreementStatusProposed {
		t.Fatalf("DurableChildAgreement = %#v, want proposed agreement for cap-family-amazon", agreement)
	}
	if len(agreement.ArtifactRefs) != 1 || agreement.ArtifactRefs[0].Kind != "review_event" {
		t.Fatalf("DurableChildAgreement artifact refs = %#v, want review_event ref", agreement.ArtifactRefs)
	}

	request, ok, err := store.CapabilityRequest("cap-family-amazon")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityRequest(cap-family-amazon) ok=false, want stored request")
	}
	if request.Kind != session.CapabilityKindPurchase || request.TargetResource != "amazon" || request.RequestedBy != "durable_agent:family-child" || request.RequestedFor != "durable_agent:family-child" {
		t.Fatalf("CapabilityRequest = %#v, want purchase request for durable_agent:family-child on amazon", request)
	}
	if request.ParentPrincipal != "telegram:200" || request.AdminPrincipal != "telegram:1001" {
		t.Fatalf("CapabilityRequest principals = parent %q admin %q, want telegram:200 and telegram:1001", request.ParentPrincipal, request.AdminPrincipal)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].Summary, "Delegation request cap-family-amazon") {
		t.Fatalf("review summary = %q, want delegation request summary", events[0].Summary)
	}
	if !strings.Contains(events[0].MetadataJSON, `"capability_request_id":"cap-family-amazon"`) || !strings.Contains(events[0].MetadataJSON, `"cart_id":"cart-1"`) {
		t.Fatalf("review metadata = %q, want capability id and copied metadata", events[0].MetadataJSON)
	}

	reportOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"delegation_report",
			"agent_id":"family-child",
			"delegation_report":{
				"request_id":"cap-family-amazon",
				"status":"blocked",
				"outcome":"cart price changed before checkout",
				"local_actions":["paused checkout"],
				"risk_flags":["spend_changed"]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(delegation_report) err = %v", err)
	}
	if !strings.Contains(reportOut, "action: durable-agent delegation report") ||
		!strings.Contains(reportOut, "request_id: cap-family-amazon") ||
		!strings.Contains(reportOut, "status: blocked") {
		t.Fatalf("delegation_report output = %q, want report summary", reportOut)
	}
	events, err = store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(after report) err = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("pending review events after report len = %d, want 2", len(events))
	}
	latest := events[len(events)-1]
	if !strings.Contains(latest.MetadataJSON, `"delegation_surface":"durable_agent.delegation_report"`) ||
		!strings.Contains(latest.MetadataJSON, `"status":"blocked"`) {
		t.Fatalf("latest review metadata = %q, want delegation report metadata", latest.MetadataJSON)
	}
}

func TestDurableAgentDelegationRequestSupportsSystemChangeKind(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "tool-learning-child",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "200",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Learn tools through parent-approved negotiation.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address: "child-endpoint",
			Adapter: "child_adapter",
		}},
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"delegation_request",
			"agent_id":"tool-learning-child",
			"delegation_request":{
				"request_id":"sys-change-learn-tool",
				"kind":"system_change",
				"target_resource":"child-tool-learning-protocol",
				"purpose":"child needs parent-approved runtime support to learn a newly authorized local tool",
				"risk_class":"system_change",
				"contract":{"child_must_explain_need":true,"parent_must_approve_before_runtime_change":true},
				"constraints":{"feature_specific_parent_code":false},
				"questions":["Approve a generic protocol change rather than hard-coding this adapter?"]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(delegation_request) err = %v", err)
	}
	if !strings.Contains(out, "kind: system_change") || !strings.Contains(out, "agreement_id: agreement-sys-change-learn-tool") {
		t.Fatalf("delegation_request output = %q, want system_change agreement", out)
	}
	request, ok, err := store.CapabilityRequest("sys-change-learn-tool")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityRequest(sys-change-learn-tool) ok=false, want stored request")
	}
	if request.Kind != session.CapabilityKindSystemChange {
		t.Fatalf("CapabilityRequest.Kind = %q, want system_change", request.Kind)
	}
}

func TestDurableAgentDelegationGrantAppliesCapabilityUpdatePlan(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	livePolicy := core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
		Charter:            "Help family members while escalating purchases and account access.",
		CapabilityEnvelope: []string{"bounded_review_artifact"},
		OutboundMode:       "read_only",
		DriftPolicy:        "admin_review",
	})
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-child",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "200",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         livePolicy,
		BootstrapCeiling: core.NormalizeDurableAgentBootstrapCeiling(core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"bounded_review_artifact", "amazon_checkout"},
			AllowedOutboundModes:         []string{"read_only", "reply_with_parent_review"},
			AllowedPublicSurfaceModes:    []string{"none", "explicit_parent_relay_only"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-test",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	parent := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 200}
	key := adminSessionKey()
	requestOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		admin,
		key,
		"durable_agent",
		json.RawMessage(`{
			"action":"delegation_request",
			"agent_id":"family-child",
			"delegation_request":{
				"request_id":"cap-family-amazon-update",
				"kind":"purchase",
				"target_resource":"amazon",
				"requested_for":"family-child",
				"purpose":"order approved school supplies",
				"risk_class":"spend",
				"contract":{"allowed":"school supplies only"},
				"constraints":{"max_usd":50},
				"grant_actions":["order"],
				"policy_patch":{
					"autonomy":"review_before_reply",
					"visibility":"parent_relay_only",
					"capabilities":["bounded_review_artifact","amazon_checkout"]
				},
				"update_reason":"approved Amazon checkout delegation"
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(delegation_request) err = %v", err)
	}
	if !strings.Contains(requestOut, "capability_update_plan: present") || !strings.Contains(requestOut, "policy_update_on_grant: true") {
		t.Fatalf("delegation_request output = %q, want embedded capability update plan", requestOut)
	}
	request, ok, err := store.CapabilityRequest("cap-family-amazon-update")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityRequest(cap-family-amazon-update) ok=false, want stored request")
	}
	if !strings.Contains(request.Contract, `"capability_update_plan"`) || !strings.Contains(request.Contract, `"amazon_checkout"`) {
		t.Fatalf("request contract = %s, want capability_update_plan with policy patch", request.Contract)
	}

	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), parent, key, "capability_authority", json.RawMessage(`{
		"action":"request_review",
		"request_id":"cap-family-amazon-update",
		"review_status":"parent_approved",
		"rationale":"bounded school supplies"
	}`)); err != nil {
		t.Fatalf("parent request_review err = %v", err)
	}
	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"request_review",
		"request_id":"cap-family-amazon-update",
		"review_status":"approved",
		"rationale":"parent endorsed"
	}`)); err != nil {
		t.Fatalf("admin request_review err = %v", err)
	}

	grantOut, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"grant_set",
		"request_id":"cap-family-amazon-update",
		"grant_id":"capg-family-amazon-update",
		"principal":"family-child"
	}`))
	if err != nil {
		t.Fatalf("grant_set err = %v", err)
	}
	if !strings.Contains(grantOut, "status: active") ||
		!strings.Contains(grantOut, "allowed_actions: order") ||
		!strings.Contains(grantOut, "policy_update_applied: true") ||
		!strings.Contains(grantOut, "policy_changed: true") {
		t.Fatalf("grant_set output = %q, want active grant and applied policy update", grantOut)
	}
	grant, ok, err := store.CapabilityGrant("capg-family-amazon-update")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityGrant(capg-family-amazon-update) ok=false, want stored grant")
	}
	if grant.Status != session.CapabilityGrantStatusActive || len(grant.AllowedActions) != 1 || grant.AllowedActions[0] != "order" {
		t.Fatalf("grant = %#v, want active grant with order action", grant)
	}
	updated, err := store.DurableAgent("family-child")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "reply_with_parent_review" {
		t.Fatalf("updated outbound_mode = %q, want reply_with_parent_review", updated.LivePolicy.OutboundMode)
	}
	if !containsString(updated.LivePolicy.CapabilityEnvelope, "amazon_checkout") {
		t.Fatalf("updated capabilities = %#v, want amazon_checkout", updated.LivePolicy.CapabilityEnvelope)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !executionEventTypeExists(events, core.ExecutionEventCapabilityUpdateApplied) {
		t.Fatalf("missing %s event", core.ExecutionEventCapabilityUpdateApplied)
	}
}

func TestDurableAgentToolConversationSendAndShow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
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
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	sendOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"conversation_send","agent_id":"child-alpha","message":"Please flag recruiter threads aggressively and keep digest concise."}`),
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
		json.RawMessage(`{"action":"conversation_show","agent_id":"child-alpha","history":5}`),
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

	state, err := store.DurableAgentState("child-alpha")
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
		AgentID:            "child-alpha",
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
	continuity = continuity.WithConversationMessage("child", "provider_failure: codex: server_is_overloaded", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   "child-alpha",
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"conversation_show","agent_id":"child-alpha","history":6}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(conversation_show) err = %v", err)
	}
	if !strings.Contains(showOut, "thread_state: retrying_after_inference_failure") {
		t.Fatalf("conversation_show output = %q, want retrying thread state", showOut)
	}
	if !strings.Contains(showOut, "last_child_error: provider_failure: codex: server_is_overloaded") {
		t.Fatalf("conversation_show output = %q, want surfaced child inference error", showOut)
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

func TestDurableAgentConnectionTestDoesNotPromoteAdapterGrantsToLiveProbe(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address: "host@idolum.ai",
			Account: "host@idolum.ai",
			Adapter: "child_adapter",
			Query:   "topic:important",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Read channel metadata and surface bounded review artifacts.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
		WakeupMode: "poll",
		Status:     "draft",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	principalID := core.DurableAgentPrincipal("child-alpha")
	for _, grant := range []session.CapabilityGrant{
		{
			GrantID:        "grant-channel-account",
			GrantedBy:      "telegram:1001",
			GrantedTo:      principalID,
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "child_adapter:host@idolum.ai",
			AllowedActions: []string{"read", "search", "metadata", "connection_test"},
			Status:         session.CapabilityGrantStatusActive,
		},
		{
			GrantID:        "grant-channel-tool",
			GrantedBy:      "telegram:1001",
			GrantedTo:      principalID,
			Kind:           session.CapabilityKindTool,
			TargetResource: "child_adapter",
			AllowedActions: []string{"invoke", "read", "search", "metadata", "connection_test"},
			Status:         session.CapabilityGrantStatusActive,
		},
	} {
		if _, err := store.UpsertCapabilityGrant(grant); err != nil {
			t.Fatalf("UpsertCapabilityGrant(%s) err = %v", grant.GrantID, err)
		}
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"connection_test","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(connection_test) err = %v", err)
	}
	if !strings.Contains(out, "status: configuration_only") {
		t.Fatalf("connection_test output = %q, want configuration_only", out)
	}
	for _, forbidden := range []string{
		"status: ok",
		"external_account_grant: grant-channel-account",
		"tool_grant: grant-channel-tool",
		"live_probe:",
	} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("connection_test output = %q, should not expose adapter-specific marker %q", out, forbidden)
		}
	}
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

func writeToolTestArchetype(t *testing.T, workspace, name string) {
	t.Helper()
	files := map[string]string{
		"AGENT.md":                  "# Aphelion Maintainer\n\nDiagnose Aphelion and propose fixes. Implementation work must happen in a /tmp clone and return as a GitHub PR.\n",
		"profile/charter.md":        "Review Aphelion sessions, memory, prompts, and code health; propose fixes with evidence.\n",
		"profile/policy.md":         "- outbound_mode: read_only\n- public_surface_mode: explicit_parent_relay_only\n- shared_inference_reuse: disabled\n- shared_inference_reuse_scope: public_prefix_only\n",
		"profile/capabilities.md":   "- session_log_read\n- repo_read\n- bounded_review_artifact\n- patch_proposal\n",
		"profile/runtime.md":        "Never mutate the local Aphelion clone. If implementation is approved, use a /tmp clone and propose the result via GitHub PR with an approved GitHub App credential.\n",
		"examples/doctor-report.md": "## State\n\nConcise diagnosis.\n",
	}
	for rel, content := range files {
		target := filepath.Join(workspace, "agents", "archetypes", name, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) err = %v", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) err = %v", target, err)
		}
	}
}
