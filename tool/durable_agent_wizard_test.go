//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

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
			"agent_id":"child-external-channel",
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

	agent, err := store.DurableAgent("child-external-channel")
	if err != nil {
		t.Fatalf("DurableAgent(child-external-channel) err = %v", err)
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
