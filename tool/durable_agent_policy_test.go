//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

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
		json.RawMessage(fmt.Sprintf(`{"action":"policy_apply","agent_id":"family-group","review_event_id":%d,"policy_overrides":{"outbound_mode":"read_only"}}`, reviewID)),
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
		json.RawMessage(`{"action":"policy_apply","agent_id":"family-group","policy_patch":{"autonomy":"review_before_reply","visibility":"parent_relay_only","shared_context":"isolated"},"reason":"ratified conversational policy"}`),
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

func TestDurableAgentToolSupportsSketchModeWithoutExternalChannelConfig(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{
			"action":"create",
			"agent_id":"child-sketch",
			"channel_kind":"external_channel",
			"policy_patch":{
				"mode":"sketch",
				"charter":"Explore a possible child shape before provisioning any adapter."
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(create sketch child) err = %v", err)
	}
	if !strings.Contains(createOut, "mode: sketch") || !strings.Contains(createOut, "wakeup_mode: manual") {
		t.Fatalf("create output = %q, want lightweight sketch summary", createOut)
	}
	activateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"activate","agent_id":"child-sketch"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(activate sketch child) err = %v", err)
	}
	if !strings.Contains(activateOut, "status: active") || !strings.Contains(activateOut, "mode: sketch") {
		t.Fatalf("activate output = %q, want active sketch child", activateOut)
	}
	agent, err := store.DurableAgent("child-sketch")
	if err != nil {
		t.Fatalf("DurableAgent(child-sketch) err = %v", err)
	}
	if agent.LivePolicy.Mode != "sketch" || agent.WakeupMode != "manual" {
		t.Fatalf("agent mode/wakeup = %q/%q, want sketch/manual", agent.LivePolicy.Mode, agent.WakeupMode)
	}
	if len(agent.LivePolicy.CapabilityEnvelope) != 1 || agent.LivePolicy.CapabilityEnvelope[0] != "bounded_review_artifact" {
		t.Fatalf("capabilities = %#v, want minimal sketch envelope", agent.LivePolicy.CapabilityEnvelope)
	}
	if external := agent.ChannelConfig.ExternalConfig(); external != nil {
		t.Fatalf("external config = %#v, want no adapter config for sketch child", external)
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
		json.RawMessage(`{"action":"policy_apply","agent_id":"Family Group durable agent","policy_patch":{"autonomy":"review_before_reply"}}`),
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
		json.RawMessage(`{"action":"policy_apply","agent_id":"missing-agent","policy_patch":{"autonomy":"observe_only"}}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(policy_apply missing agent) err = nil, want helpful not-found error")
	}
	if !strings.Contains(err.Error(), "available agent_ids: family-group") {
		t.Fatalf("err = %v, want available agent list", err)
	}
}
