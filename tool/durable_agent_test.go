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
	if !strings.Contains(createOut, "email_address: idolum@example.com") {
		t.Fatalf("create output = %q, want email address summary", createOut)
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

func TestDurableAgentToolEmailWizardHappyPath(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)

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
