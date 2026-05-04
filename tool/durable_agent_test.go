//go:build linux

package tool

import (
	"context"
	"encoding/json"
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
	if !strings.Contains(durableDefJSON, `"bootstrap_update"`) || !strings.Contains(durableDefJSON, `"bootstrap_llm"`) {
		t.Fatalf("durable_agent definition missing bootstrap update surface: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"wizard_answers"`) {
		t.Fatalf("durable_agent definition missing wizard_answers field: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"access_grant"`) || !strings.Contains(durableDefJSON, `"telegram_user_ids"`) {
		t.Fatalf("durable_agent definition missing access control surface: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"conversation_show"`) || !strings.Contains(durableDefJSON, `"conversation_send"`) || !strings.Contains(durableDefJSON, `"message"`) {
		t.Fatalf("durable_agent definition missing conversation surface: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"delegation_request"`) || !strings.Contains(durableDefJSON, `"delegation_report"`) {
		t.Fatalf("durable_agent definition missing generic delegation surface: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"generic_delegation"`) || !strings.Contains(durableDefJSON, `"system_change"`) || !strings.Contains(durableDefJSON, `"purchase"`) || !strings.Contains(durableDefJSON, `"local_device"`) {
		t.Fatalf("durable_agent definition missing capability kind delegation enum: %s", durableDefJSON)
	}
	if !strings.Contains(durableDefJSON, `"capability_update_plan"`) || !strings.Contains(durableDefJSON, `"grant_actions"`) {
		t.Fatalf("durable_agent definition missing capability update plan surface: %s", durableDefJSON)
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
	continuity = continuity.WithConversationMessage("child", "Inference backends are unavailable after retries and fallback. This turn did not complete. You can /stop to cancel current work and try again.", time.Now().UTC().Add(-time.Minute))
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
	if !strings.Contains(showOut, "last_child_error: Inference backends are unavailable after retries and fallback.") {
		t.Fatalf("conversation_show output = %q, want surfaced child inference error", showOut)
	}
}
func TestDurableAgentToolBootstrapShowIncludesStateAndHistory(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"})
	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy:         defaultDurableAgentLivePolicy("external_channel", "Read-only external child."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("external_channel", defaultDurableAgentLivePolicy("external_channel", "Read-only external child.")),
		BootstrapLLM:       core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
		LocalStorageRoots:  []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:      "default",
		WakeupMode:         "poll",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if _, _, err := store.ApplyDurableAgentBootstrap(agent.AgentID, core.NodeLLMBootstrap{Backend: "native", NativeProvider: "anthropic", APIKey: "sk-child", Model: "claude-child"}, 0, 1001, string(principal.RoleAdmin), "explicit", "switch away from parent"); err != nil {
		t.Fatalf("ApplyDurableAgentBootstrap() err = %v", err)
	}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, adminSessionKey(), "durable_agent", json.RawMessage(`{"action":"bootstrap_show","agent_id":"child-alpha","history":5}`))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(bootstrap_show) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent bootstrap show") || !strings.Contains(out, "history_count: 1") || !strings.Contains(out, "bootstrap_source_hint: pinned_or_diverged") {
		t.Fatalf("bootstrap_show output = %q, want state and history", out)
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

func TestDurableAgentToolBootstrapUpdateExplicitAndInherit(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{
		Backend:         "codex",
		CodexAuthSource: "codex_cli",
		CodexHome:       "/tmp/codex-home",
	})
	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy:         defaultDurableAgentLivePolicy("external_channel", "Read-only external child."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("external_channel", defaultDurableAgentLivePolicy("external_channel", "Read-only external child.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-old",
			Model:          "claude-old",
		},
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "poll",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: `{"capability_contract":{"status":"verified"}}`}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"bootstrap_update","agent_id":"child-alpha","reason":"switch to codex","bootstrap_llm":{"backend":"codex","codex_auth_source":"codex_cli","codex_home":"/srv/codex-child"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(bootstrap_update explicit) err = %v", err)
	}
	if !strings.Contains(out, "changed: true") || !strings.Contains(out, "new_bootstrap_backend: codex") {
		t.Fatalf("bootstrap_update output = %q, want codex change", out)
	}
	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.BootstrapLLM.Backend != "codex" || updated.BootstrapLLM.CodexHome != "/srv/codex-child" {
		t.Fatalf("updated BootstrapLLM = %#v, want codex /srv/codex-child", updated.BootstrapLLM)
	}
	updates, err := store.DurableAgentBootstrapUpdates(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("DurableAgentBootstrapUpdates() err = %v", err)
	}
	if len(updates) != 1 || updates[0].UpdateKind != "explicit" || updates[0].ActorUserID != 1001 {
		t.Fatalf("bootstrap updates = %#v, want one explicit update by admin 1001", updates)
	}
	if updates[0].PreviousBootstrap.APIKey != "" {
		t.Fatalf("bootstrap updates[0].PreviousBootstrap.APIKey = %q, want redacted empty value", updates[0].PreviousBootstrap.APIKey)
	}

	out, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"bootstrap_update","agent_id":"child-alpha","reason":"inherit parent codex","bootstrap_profile":"inherit_parent"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(bootstrap_update inherit) err = %v", err)
	}
	if !strings.Contains(out, "update_kind: inherit_parent") {
		t.Fatalf("bootstrap_update inherit output = %q, want inherit_parent kind", out)
	}
	updated, err = store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() after inherit err = %v", err)
	}
	if updated.BootstrapLLM.Backend != "codex" || updated.BootstrapLLM.CodexHome != "/tmp/codex-home" {
		t.Fatalf("updated inherited BootstrapLLM = %#v, want parent codex bootstrap", updated.BootstrapLLM)
	}
}
func TestDurableAgentToolBootstrapUpdateHistoryRedactsAPIKeys(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy:         defaultDurableAgentLivePolicy("external_channel", "Read-only external child."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("external_channel", defaultDurableAgentLivePolicy("external_channel", "Read-only external child.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-old",
			Model:          "claude-old",
		},
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "poll",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"bootstrap_update","agent_id":"child-alpha","reason":"rotate native model+key","bootstrap_llm":{"backend":"native","native_provider":"anthropic","api_key":"sk-new","model":"claude-new"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(bootstrap_update explicit native) err = %v", err)
	}

	updates, err := store.DurableAgentBootstrapUpdates(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("DurableAgentBootstrapUpdates() err = %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("bootstrap updates = %#v, want exactly one history entry", updates)
	}
	if updates[0].PreviousBootstrap.APIKey != "" || updates[0].NewBootstrap.APIKey != "" {
		t.Fatalf("bootstrap history leaked api keys: previous=%q new=%q", updates[0].PreviousBootstrap.APIKey, updates[0].NewBootstrap.APIKey)
	}
	if updates[0].PreviousBootstrap.Model != "claude-old" || updates[0].NewBootstrap.Model != "claude-new" {
		t.Fatalf("bootstrap history models = (%q,%q), want (claude-old,claude-new)", updates[0].PreviousBootstrap.Model, updates[0].NewBootstrap.Model)
	}
}

func TestDurableAgentToolBootstrapUpdateRequiresReasonAndOneSource(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"})
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy:         defaultDurableAgentLivePolicy("external_channel", "Read-only external child."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("external_channel", defaultDurableAgentLivePolicy("external_channel", "Read-only external child.")),
		BootstrapLLM:       core.NodeLLMBootstrap{Backend: "native", NativeProvider: "anthropic", APIKey: "sk-old", Model: "claude-old"},
		LocalStorageRoots:  []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:      "default",
		WakeupMode:         "poll",
		Status:             "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"bootstrap_update","agent_id":"child-alpha","bootstrap_profile":"inherit_parent"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("bootstrap_update missing reason err = %v, want reason required", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"bootstrap_update","agent_id":"child-alpha","reason":"bad","bootstrap_profile":"inherit_parent","bootstrap_llm":{"backend":"codex","codex_auth_source":"codex_cli","codex_home":"/srv/codex-child"}}`),
	)
	if err == nil || !strings.Contains(err.Error(), "either bootstrap_profile or bootstrap_llm") {
		t.Fatalf("bootstrap_update dual source err = %v, want exclusivity error", err)
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
				"outbound_mode":"read_only",
				"tailnet_mode":"tsnet",
				"tailnet_hostname":"family-helper",
				"tailnet_tags":["tag:aphelion-child","tag:family"],
				"tailnet_surface_policy":"private_status"
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
	if !strings.Contains(out, "tailnet_mode: tsnet") {
		t.Fatalf("policy apply output = %q, want tailnet declaration summary", out)
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
	if updated.LivePolicy.TailnetMode != "tsnet" || updated.LivePolicy.TailnetHostname != "family-helper" || updated.LivePolicy.TailnetSurfacePolicy != "private_status" {
		t.Fatalf("updated tailnet declaration = %#v, want family helper declaration", updated.LivePolicy)
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

func TestDurableAgentToolCreateAndActivateExternalChannelDraft(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)

	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"create",
			"agent_id":"child-alpha",
			"channel_kind":"external_channel",
			"charter":"Review the external channel, surface important threads, summarize PDFs, and never send outbound messages.",
			"autonomy":"observe_only",
			"capabilities":["read_channel","bounded_review_artifact","summarize_pdf"],
			"wakeup_mode":"poll",
			"secret_scopes":["child_adapter"],
			"channel_config":{
				"external":{
					"address":"idolum@example.com",
					"account":"idolum@example.com",
					"adapter":"child_adapter",
					"query":"topic:important newer_than:7d",
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
		t.Fatalf("ExecuteForSessionPrincipal(create external-channel draft) err = %v", err)
	}
	if !strings.Contains(createOut, "action: durable-agent create") || !strings.Contains(createOut, "status: draft") {
		t.Fatalf("create output = %q, want durable-agent create draft summary", createOut)
	}
	if !strings.Contains(createOut, "channel_kind: external_channel") || !strings.Contains(createOut, "channel_profile: external") {
		t.Fatalf("create output = %q, want external channel kind/profile", createOut)
	}
	if !strings.Contains(createOut, "channel_address: idolum@example.com") {
		t.Fatalf("create output = %q, want channel address summary alias", createOut)
	}

	draft, err := store.DurableAgent("child-alpha")
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
	if external := draft.ChannelConfig.ExternalConfig(); external == nil || external.Adapter != "child_adapter" {
		t.Fatalf("ChannelConfig.ExternalConfig() = %#v, want child_adapter channel config", external)
	}
	if draft.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("LivePolicy.OutboundMode = %q, want read_only", draft.LivePolicy.OutboundMode)
	}

	activateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"activate","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(activate external-channel draft) err = %v", err)
	}
	if !strings.Contains(activateOut, "action: durable-agent activate") || !strings.Contains(activateOut, "status: active") {
		t.Fatalf("activate output = %q, want activation summary", activateOut)
	}

	activated, err := store.DurableAgent("child-alpha")
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

func TestDurableAgentToolCreateSupportsExternalChannelConfig(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)

	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"create",
			"agent_id":"child-legacy-alias",
			"channel_kind":"external_channel",
			"wakeup_mode":"poll",
			"channel_config":{
				"external":{
					"address":"idolum@example.com",
					"adapter":"child_adapter"
				}
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(create external channel) err = %v", err)
	}
	if !strings.Contains(createOut, "channel_kind: external_channel") || !strings.Contains(createOut, "channel_profile: external") {
		t.Fatalf("create output = %q, want canonical external channel kind/profile", createOut)
	}

	agent, err := store.DurableAgent("child-legacy-alias")
	if err != nil {
		t.Fatalf("DurableAgent(child-legacy-alias) err = %v", err)
	}
	if agent.ChannelKind != "external_channel" {
		t.Fatalf("ChannelKind = %q, want canonical external_channel", agent.ChannelKind)
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		t.Fatal("ChannelConfig.ExternalConfig() = nil, want normalized external channel config")
	}
	if external.Address != "idolum@example.com" {
		t.Fatalf("ChannelConfig.ExternalConfig().Address = %q, want idolum@example.com", external.Address)
	}
	if external.Adapter != "child_adapter" {
		t.Fatalf("ChannelConfig.ExternalConfig().Adapter = %q, want child_adapter", external.Adapter)
	}
}

func TestDurableAgentToolExternalChannelWizardHappyPath(t *testing.T) {
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
		json.RawMessage(`{"action":"wizard_start","agent_id":"child-alpha","channel_kind":"external_channel"}`),
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
			"agent_id":"child-alpha",
			"wizard_answers":{
				"address":"idolum@example.com",
				"account":"idolum@example.com",
				"adapter":"child_adapter",
				"bootstrap_profile":"child_custom",
				"bootstrap_model":"claude-sonnet-4-6",
				"query":"topic:important newer_than:7d",
				"charter":"Review the external channel, surface important threads, summarize PDFs, and never send outbound messages.",
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
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"child-alpha"}`),
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

	agent, err := store.DurableAgent("child-alpha")
	if err != nil {
		t.Fatalf("DurableAgent(child-alpha) err = %v", err)
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
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		t.Fatal("agent external channel_config = nil, want configured child channel")
	}
	if external.SynthesisCadence != "4h" {
		t.Fatalf("channel synthesis_cadence = %q, want 4h", external.SynthesisCadence)
	}
	if len(external.NeverRetain) != 2 || external.NeverRetain[0] != "oauth_token" {
		t.Fatalf("channel never_retain = %#v, want oauth_token/password", external.NeverRetain)
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

func TestDurableAgentToolExternalChannelWizardFinalizeRequiresCompleteAnswers(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_start","agent_id":"child-alpha","channel_kind":"external_channel"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_start) err = %v", err)
	}
	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_answer","agent_id":"child-alpha","wizard_answers":{"address":"idolum@example.com","adapter":"child_adapter"}}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_answer partial) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"child-alpha"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(wizard_finalize partial) err = nil, want missing-answer error")
	}
	if !strings.Contains(err.Error(), "missing wizard answers") {
		t.Fatalf("err = %v, want missing wizard answers guidance", err)
	}
}

func TestDurableAgentToolExternalChannelWizardChildCustomRequiresBootstrapModel(t *testing.T) {
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
		json.RawMessage(`{"action":"wizard_start","agent_id":"child-alpha","channel_kind":"external_channel"}`),
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
			"agent_id":"child-alpha",
			"wizard_answers":{
				"address":"idolum@example.com",
				"adapter":"child_adapter",
				"bootstrap_profile":"child_custom",
				"charter":"Read-only external child.",
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
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"child-alpha"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(wizard_finalize without bootstrap_model) err = nil, want missing-answer error")
	}
	if !strings.Contains(err.Error(), "bootstrap_model") {
		t.Fatalf("err = %v, want missing bootstrap_model guidance", err)
	}
}

func TestDurableAgentToolExternalChannelWizardBootstrapInheritanceAndCustomModel(t *testing.T) {
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
		json.RawMessage(`{"action":"wizard_start","agent_id":"child-alpha","channel_kind":"external_channel"}`),
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
			"agent_id":"child-alpha",
			"wizard_answers":{
				"address":"idolum@example.com",
				"adapter":"child_adapter",
				"bootstrap_profile":"child_custom",
				"bootstrap_model":"claude-custom-child",
				"charter":"Read-only external child.",
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
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"child-alpha"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_finalize custom bootstrap) err = %v", err)
	}

	agent, err := store.DurableAgent("child-alpha")
	if err != nil {
		t.Fatalf("DurableAgent(child-alpha) err = %v", err)
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

func TestDurableAgentToolExternalChannelWizardCodexChildCustomDoesNotRequireBootstrapModel(t *testing.T) {
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
		json.RawMessage(`{"action":"wizard_start","agent_id":"child-alpha","channel_kind":"external_channel"}`),
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
			"agent_id":"child-alpha",
			"wizard_answers":{
				"address":"idolum@example.com",
				"adapter":"child_adapter",
				"bootstrap_profile":"child_custom",
				"charter":"Read-only external child.",
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
		json.RawMessage(`{"action":"wizard_finalize","agent_id":"child-alpha"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wizard_finalize codex child_custom) err = %v", err)
	}

	agent, err := store.DurableAgent("child-alpha")
	if err != nil {
		t.Fatalf("DurableAgent(child-alpha) err = %v", err)
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

func TestDurableAgentArtifactPutWritesChildSpecificArtifactHome(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "artifact-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "child", "workspace"), childMemory},
		Status:            "active",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	input, err := json.Marshal(durableAgentInput{
		Action:  "artifact_put",
		AgentID: "artifact-child",
		Artifact: &durableAgentArtifactInput{
			Path:    "schemas/console_status.schema.json",
			Kind:    "schema",
			Reason:  "child-owned status contract",
			Content: "{\n  \"type\": \"object\"\n}\n",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		input,
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(artifact_put) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent artifact put") ||
		!strings.Contains(out, "written: artifacts/schemas/console_status.schema.json") ||
		!strings.Contains(out, "sha256:") {
		t.Fatalf("artifact_put output = %q, want written artifact summary", out)
	}
	raw, err := os.ReadFile(filepath.Join(childMemory, "artifacts", "schemas", "console_status.schema.json"))
	if err != nil {
		t.Fatalf("ReadFile(artifact) err = %v", err)
	}
	if string(raw) != "{\n  \"type\": \"object\"\n}\n" {
		t.Fatalf("artifact content = %q, want exact child-specific content", raw)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(childMemory, "artifacts", "ARTIFACTS.json"))
	if err != nil {
		t.Fatalf("ReadFile(manifest) err = %v", err)
	}
	for _, needle := range []string{
		`"agent_id": "artifact-child"`,
		`"path": "schemas/console_status.schema.json"`,
		`"kind": "schema"`,
		`"source": "parent_governed_artifact"`,
		`"reason": "child-owned status contract"`,
	} {
		if !strings.Contains(string(manifestRaw), needle) {
			t.Fatalf("manifest = %s, want %s", manifestRaw, needle)
		}
	}

	listOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"artifact_list","agent_id":"artifact-child"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(artifact_list) err = %v", err)
	}
	if !strings.Contains(listOut, "count: 1") || !strings.Contains(listOut, "path=artifacts/schemas/console_status.schema.json") {
		t.Fatalf("artifact_list output = %q, want artifact entry", listOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"artifact_show","agent_id":"artifact-child","artifact":{"path":"schemas/console_status.schema.json"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(artifact_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent artifact show") ||
		!strings.Contains(showOut, "content:\n{\n  \"type\": \"object\"\n}") {
		t.Fatalf("artifact_show output = %q, want artifact content", showOut)
	}
}

func TestDurableAgentArtifactPutRejectsEscapingPath(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "artifact-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "child", "workspace"), childMemory},
		Status:            "active",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	input, err := json.Marshal(durableAgentInput{
		Action:  "artifact_put",
		AgentID: "artifact-child",
		Artifact: &durableAgentArtifactInput{
			Path:    "../core/console_status.go",
			Content: "package core\n",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		input,
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(artifact_put) err = nil, want escaping path error")
	}
	if !strings.Contains(err.Error(), "artifact path") {
		t.Fatalf("err = %v, want artifact path context", err)
	}
}

func TestDurableAgentDefinitionIncludesArtifactActions(t *testing.T) {
	registry, _ := newDurableAgentToolRegistry(t)
	var durableDef string
	for _, def := range registry.Definitions() {
		if def.Name == "durable_agent" {
			durableDef = string(def.Parameters)
			break
		}
	}
	if durableDef == "" {
		t.Fatal("durable_agent definition not found")
	}
	for _, needle := range []string{`"artifact_put"`, `"artifact_list"`, `"artifact_show"`, `"artifact"`, `"archetype_list"`, `"archetype_show"`, `"create_from_archetype"`, `"archetype"`} {
		if !strings.Contains(durableDef, needle) {
			t.Fatalf("durable_agent definition missing %s: %s", needle, durableDef)
		}
	}
}

func TestDurableAgentArchetypeListShowAndCreate(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	writeToolTestArchetype(t, registry.workspace, "aphelion-maintainer")
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"})

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	listOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"archetype_list"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(archetype_list) err = %v", err)
	}
	if !strings.Contains(listOut, "action: durable-agent archetype list") || !strings.Contains(listOut, "aphelion-maintainer") {
		t.Fatalf("archetype_list output = %q, want maintainer archetype", listOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"archetype_show","archetype":"aphelion-maintainer"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(archetype_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent archetype show") ||
		!strings.Contains(showOut, "required_files:") ||
		!strings.Contains(showOut, "examples/doctor-report.md") {
		t.Fatalf("archetype_show output = %q, want archetype summary", showOut)
	}
	if !strings.Contains(showOut, "/tmp clone") || !strings.Contains(showOut, "GitHub PR") {
		t.Fatalf("archetype_show output = %q, want clone/PR boundary", showOut)
	}

	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"create_from_archetype","agent_id":"aphelion-maintainer-live","archetype":"aphelion-maintainer"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(create_from_archetype) err = %v", err)
	}
	if !strings.Contains(createOut, "action: durable-agent archetype create") ||
		!strings.Contains(createOut, "status: draft") ||
		!strings.Contains(createOut, "archetype: aphelion-maintainer") {
		t.Fatalf("create_from_archetype output = %q, want archetype create summary", createOut)
	}

	agent, err := store.DurableAgent("aphelion-maintainer-live")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if agent.Status != "draft" {
		t.Fatalf("agent status = %q, want draft", agent.Status)
	}
	if agent.LivePolicy.OutboundMode != "read_only" || !containsString(agent.LivePolicy.CapabilityEnvelope, "session_log_read") {
		t.Fatalf("agent policy = %+v, want read-only session-log posture", agent.LivePolicy)
	}
	memoryRoot, err := durableAgentMemoryRoot(*agent, store)
	if err != nil {
		t.Fatalf("durableAgentMemoryRoot() err = %v", err)
	}
	provenanceRaw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "ARCHETYPE.json"))
	if err != nil {
		t.Fatalf("ReadFile(ARCHETYPE.json) err = %v", err)
	}
	if !strings.Contains(string(provenanceRaw), `"name": "aphelion-maintainer"`) {
		t.Fatalf("ARCHETYPE.json = %s, want archetype provenance", provenanceRaw)
	}
	copiedAgentRaw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "archetype", "AGENT.md"))
	if err != nil {
		t.Fatalf("ReadFile(archetype AGENT.md) err = %v", err)
	}
	if !strings.Contains(string(copiedAgentRaw), "Aphelion Maintainer") {
		t.Fatalf("copied AGENT.md = %q, want archetype template copy", copiedAgentRaw)
	}
	runtimeRaw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "archetype", "profile", "runtime.md"))
	if err != nil {
		t.Fatalf("ReadFile(archetype runtime.md) err = %v", err)
	}
	if !strings.Contains(string(runtimeRaw), "/tmp clone") || !strings.Contains(string(runtimeRaw), "GitHub PR") {
		t.Fatalf("copied runtime.md = %q, want clone/PR boundary", runtimeRaw)
	}
	if _, err := os.Stat(filepath.Join(registry.workspace, "core", "aphelion_maintainer.go")); !os.IsNotExist(err) {
		t.Fatalf("unexpected repo-specific child file err = %v", err)
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

func TestDurableAgentPolicyApplySyncsProfileFiles(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	childWorkspace := filepath.Join(t.TempDir(), "child", "workspace")
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "profile-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{childWorkspace, childMemory},
		Status:            "active",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"bounded_review_artifact", "session_recall"},
			AllowedOutboundModes:         []string{"read_only", "reply_with_policy_authorization"},
			AllowedPublicSurfaceModes:    []string{"none", "explicit_parent_relay_only"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Initial charter.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	_, err := registry.applyDurableAgentPolicy(durableAgentInput{
		AgentID: "profile-child",
		PolicyPatch: &durableAgentPolicyPatchInput{
			Charter:      "Updated child charter.",
			Capabilities: []string{"bounded_review_artifact", "session_recall"},
		},
		Reason: "ratify profile files",
	})
	if err != nil {
		t.Fatalf("applyDurableAgentPolicy() err = %v", err)
	}
	charterRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "charter.md"))
	if err != nil {
		t.Fatalf("ReadFile(charter) err = %v", err)
	}
	capRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "capabilities.md"))
	if err != nil {
		t.Fatalf("ReadFile(capabilities) err = %v", err)
	}
	runtimeRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "runtime.md"))
	if err != nil {
		t.Fatalf("ReadFile(runtime) err = %v", err)
	}
	growthRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "growth.md"))
	if err != nil {
		t.Fatalf("ReadFile(growth) err = %v", err)
	}
	ledgerRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "capability-ledger.md"))
	if err != nil {
		t.Fatalf("ReadFile(capability-ledger) err = %v", err)
	}
	scorecardRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "scorecard.md"))
	if err != nil {
		t.Fatalf("ReadFile(scorecard) err = %v", err)
	}
	if !strings.Contains(string(charterRaw), "Updated child charter.") ||
		!strings.Contains(string(capRaw), "session_recall") ||
		!strings.Contains(string(runtimeRaw), "child_runtime") ||
		!strings.Contains(string(growthRaw), "delegation_request") ||
		!strings.Contains(string(growthRaw), "json.loads") ||
		!strings.Contains(string(ledgerRaw), "Active grants:") ||
		!strings.Contains(string(scorecardRaw), "Accurate statements") {
		t.Fatalf("profile files missing ratified content: charter=%q capabilities=%q runtime=%q", charterRaw, capRaw, runtimeRaw)
	}
}

func TestDurableAgentProfileSyncIncludesTailnetDeclaration(t *testing.T) {
	t.Parallel()

	_, store := newDurableAgentToolRegistry(t)
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "tailnet-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "child", "workspace"), childMemory},
		Status:            "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:              "Tailnet-aware helper.",
			OutboundMode:         "read_only",
			DriftPolicy:          "admin_review",
			TailnetMode:          "tsnet",
			TailnetHostname:      "tailnet-helper",
			TailnetTags:          []string{"tag:aphelion-child"},
			TailnetSurfacePolicy: "private_status",
		}),
	}

	if _, err := syncDurableAgentProfileFiles(agent, store); err != nil {
		t.Fatalf("syncDurableAgentProfileFiles() err = %v", err)
	}
	policyRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "policy.md"))
	if err != nil {
		t.Fatalf("ReadFile(policy) err = %v", err)
	}
	runtimeRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "runtime.md"))
	if err != nil {
		t.Fatalf("ReadFile(runtime) err = %v", err)
	}
	surfaceRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "surface-rules.md"))
	if err != nil {
		t.Fatalf("ReadFile(surface-rules) err = %v", err)
	}
	for name, raw := range map[string]string{
		"policy.md":        string(policyRaw),
		"runtime.md":       string(runtimeRaw),
		"surface-rules.md": string(surfaceRaw),
	} {
		if !strings.Contains(raw, "tailnet_mode: tsnet") || !strings.Contains(raw, "tailnet_hostname: tailnet-helper") {
			t.Fatalf("%s = %q, want child tailnet declaration", name, raw)
		}
	}
	if !strings.Contains(string(surfaceRaw), "declared only") || !strings.Contains(string(surfaceRaw), "verify actual materialization") {
		t.Fatalf("surface-rules.md = %q, want declared-only materialization warning", string(surfaceRaw))
	}
}

func TestDurableAgentProfileApplyWritesChildAuthoredManifestEntry(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	memoryRoot := filepath.Join(t.TempDir(), "memory")
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:           "script-scout",
		ChannelKind:       "external_channel",
		Status:            "active",
		PolicyHash:        "policy-hash-1",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "native", NativeProvider: "openrouter", APIKey: "sk-test", Model: "test-model"},
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), memoryRoot},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, adminSessionKey(), "durable_agent", json.RawMessage(`{
		"action":"profile_apply",
		"agent_id":"script-scout",
		"profile_edit":{"target_file":"persona.md","content":"Curious scout. Asks before synthesizing.","reason":"seed scout voice"}
	}`))
	if err != nil {
		t.Fatalf("profile_apply err = %v", err)
	}
	if !strings.Contains(out, "profile/PROFILE.json") || !strings.Contains(out, "ownership=child_authored") {
		t.Fatalf("profile_apply output = %q, want child-authored manifest entry", out)
	}
	raw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "persona.md"))
	if err != nil {
		t.Fatalf("ReadFile(persona.md) err = %v", err)
	}
	if !strings.Contains(string(raw), "profile_ownership: child_authored") || !strings.Contains(string(raw), "Curious scout") {
		t.Fatalf("persona.md = %q, want child-authored profile content", string(raw))
	}
}
