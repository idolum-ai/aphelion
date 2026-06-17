//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

func TestRuntimeModelSlotOverrideRoutesGovernorExecution(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Providers.Anthropic.APIKey = "test-key"
	cfg.Providers.Anthropic.Model = "claude-sonnet-4-6"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	status, err := rt.SetModelSlotOverride(core.ModelSlotConfig{
		Slot:      core.ModelSlotGovernor,
		Provider:  core.ModelProviderAnthropic,
		Model:     "claude-opus-4-8",
		Effort:    "high",
		Transport: core.ModelTransportAuto,
	}, "test", "route governor")
	if err != nil {
		t.Fatalf("SetModelSlotOverride() err = %v", err)
	}
	if status.Validation.ResolvedTransport != core.ModelTransportAnthropicMessages {
		t.Fatalf("resolved transport = %q, want anthropic_messages", status.Validation.ResolvedTransport)
	}

	exec := rt.executionForTurn(pipeline.TurnPrepareContract{UserText: "hello", LedgerText: "hello"})
	if exec.Provider == nil {
		t.Fatal("exec provider nil")
	}
	if exec.Provider == provider {
		t.Fatal("exec provider still uses original native provider, want slot provider")
	}
	if exec.ProviderName != core.ModelProviderAnthropic || exec.ModelName != "claude-opus-4-8" {
		t.Fatalf("provider/model = %s/%s, want anthropic/claude-opus-4-8", exec.ProviderName, exec.ModelName)
	}
	opts := rt.reasoningOptionsForRun(session.TurnRunKindInteractive)
	if opts == nil || opts.Reasoning.Effort != agent.ReasoningEffortHigh {
		t.Fatalf("reasoning effort = %#v, want high", opts)
	}
}

func TestRuntimeModelSlotOverrideDoesNotMutateRecipeDefault(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Providers.Anthropic.APIKey = "test-key"
	cfg.Providers.Anthropic.Model = "claude-sonnet-4-6"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	defaultBefore, err := rt.EffectiveModelSlot(core.ModelSlotGovernor)
	if err != nil {
		t.Fatalf("EffectiveModelSlot(before) err = %v", err)
	}
	before := rt.currentRecipeSnapshot()

	if _, err := rt.SetModelSlotOverride(core.ModelSlotConfig{
		Slot:      core.ModelSlotGovernor,
		Provider:  core.ModelProviderAnthropic,
		Model:     "claude-opus-4-8",
		Effort:    "xhigh",
		Transport: core.ModelTransportAuto,
	}, "test", "temporary investigation"); err != nil {
		t.Fatalf("SetModelSlotOverride() err = %v", err)
	}
	if _, err := rt.ClearModelSlot(core.ModelSlotGovernor, "test", "done"); err != nil {
		t.Fatalf("ClearModelSlot() err = %v", err)
	}

	after := rt.currentRecipeSnapshot()
	if after != before {
		t.Fatalf("recipe snapshot = %#v, want unchanged %#v", after, before)
	}
	status, err := rt.EffectiveModelSlot(core.ModelSlotGovernor)
	if err != nil {
		t.Fatalf("EffectiveModelSlot() err = %v", err)
	}
	if status.Source != "default" ||
		status.Effective.Provider != defaultBefore.Effective.Provider ||
		status.Effective.Model != defaultBefore.Effective.Model ||
		status.Effective.Effort != defaultBefore.Effective.Effort ||
		status.Effective.Transport != defaultBefore.Effective.Transport ||
		status.Effective.ServiceTier != defaultBefore.Effective.ServiceTier {
		t.Fatalf("status = %#v, want default governor %#v after clear", status, defaultBefore)
	}
}

func TestRuntimeCheapModelSlotsPreferOpenAIMini(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Providers.OpenAI.APIKey = "test-key"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	for _, slot := range []string{core.ModelSlotStatusReadable, core.ModelSlotHeartbeat, core.ModelSlotCuriosity} {
		status, err := rt.EffectiveModelSlot(slot)
		if err != nil {
			t.Fatalf("EffectiveModelSlot(%s) err = %v", slot, err)
		}
		if status.Source != "default" {
			t.Fatalf("%s source = %q, want default", slot, status.Source)
		}
		if status.Effective.Provider != core.ModelProviderOpenAI || status.Effective.Model != "gpt-5.4-mini" || status.Effective.Effort != "low" {
			t.Fatalf("%s effective = %#v, want openai/gpt-5.4-mini effort=low", slot, status.Effective)
		}
		if !status.Validation.Valid {
			t.Fatalf("%s validation invalid: %s", slot, status.Validation.Error)
		}
	}
}

func TestRuntimeCheapModelSlotsFallBackToAnthropicHaiku(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Providers.Anthropic.APIKey = "test-key"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	status, err := rt.EffectiveModelSlot(core.ModelSlotCuriosity)
	if err != nil {
		t.Fatalf("EffectiveModelSlot(curiosity) err = %v", err)
	}
	if status.Effective.Provider != core.ModelProviderAnthropic || status.Effective.Model != "claude-haiku-4-5-20251001" || status.Effective.Effort != "low" {
		t.Fatalf("curiosity default = %#v, want anthropic/claude-haiku-4-5-20251001 effort=low", status.Effective)
	}
}

func TestRuntimeHeartbeatSlotRoutesExecutionAndReasoning(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Providers.OpenAI.APIKey = "test-key"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	slotProvider := &fakeProvider{replyText: "heartbeat"}
	var built core.ModelSlotConfig
	rt.buildProviderHook = func(_ *config.Config, slot core.ModelSlotConfig) (agent.Provider, error) {
		built = slot
		return slotProvider, nil
	}

	exec := rt.executionForTurn(pipeline.TurnPrepareContract{UserText: "heartbeat", LedgerText: "heartbeat"})
	rt.applyModelSlotExecutionIncludingDefault(&exec, core.ModelSlotHeartbeat)
	if exec.Provider != slotProvider {
		t.Fatalf("exec provider = %#v, want heartbeat slot provider", exec.Provider)
	}
	if exec.ProviderName != core.ModelProviderOpenAI || exec.ModelName != "gpt-5.4-mini" {
		t.Fatalf("exec provider/model = %s/%s, want openai/gpt-5.4-mini", exec.ProviderName, exec.ModelName)
	}
	if built.Slot != core.ModelSlotHeartbeat {
		t.Fatalf("built slot = %q, want heartbeat", built.Slot)
	}
	opts := rt.reasoningOptionsForRun(session.TurnRunKindHeartbeat)
	if opts == nil || opts.Reasoning.Effort != agent.ReasoningEffortLow {
		t.Fatalf("heartbeat reasoning = %#v, want low", opts)
	}

	if _, err := rt.SetModelSlotOverride(core.ModelSlotConfig{
		Slot:      core.ModelSlotHeartbeat,
		Provider:  core.ModelProviderOpenAI,
		Model:     "gpt-5.5",
		Effort:    "medium",
		Transport: core.ModelTransportAuto,
	}, "test", "raise heartbeat"); err != nil {
		t.Fatalf("SetModelSlotOverride(heartbeat) err = %v", err)
	}
	opts = rt.reasoningOptionsForRun(session.TurnRunKindHeartbeat)
	if opts == nil || opts.Reasoning.Effort != agent.ReasoningEffortMedium {
		t.Fatalf("heartbeat override reasoning = %#v, want medium", opts)
	}
}

func TestRuntimeCuriositySlotProviderCanRunWithoutMainProvider(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	cfg.Providers.OpenAI.APIKey = "test-key"
	rt := &Runtime{cfg: cfg, store: store, outbound: sender}
	slotProvider := &fakeProvider{replyText: "curiosity"}
	rt.buildProviderHook = func(_ *config.Config, slot core.ModelSlotConfig) (agent.Provider, error) {
		if slot.Slot != core.ModelSlotCuriosity {
			t.Fatalf("slot = %q, want curiosity", slot.Slot)
		}
		return slotProvider, nil
	}

	provider, status, ok := rt.modelSlotProviderIncludingDefault(core.ModelSlotCuriosity)
	if !ok {
		t.Fatalf("curiosity slot unavailable: %#v", status)
	}
	if provider != slotProvider {
		t.Fatalf("provider = %#v, want curiosity slot provider", provider)
	}
}

func TestStatusReadableSummaryUsesStatusSlot(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Providers.OpenAI.APIKey = "test-key"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	statusProvider := &fakeProvider{replyText: "Summary: Healthy enough to continue."}
	var built core.ModelSlotConfig
	rt.buildProviderHook = func(_ *config.Config, slot core.ModelSlotConfig) (agent.Provider, error) {
		built = slot
		return statusProvider, nil
	}

	got := rt.StatusReadableSummary(context.Background(), "chat", "status_scope=chat\nsummary state=ready")
	if !strings.Contains(got, "Healthy enough to continue") {
		t.Fatalf("StatusReadableSummary() = %q, want status slot provider summary", got)
	}
	if built.Slot != core.ModelSlotStatusReadable || built.Model != "gpt-5.4-mini" {
		t.Fatalf("built slot = %#v, want status gpt-5.4-mini", built)
	}
	statusProvider.mu.Lock()
	effort := statusProvider.lastReasoning.Effort
	statusProvider.mu.Unlock()
	if effort != agent.ReasoningEffortLow {
		t.Fatalf("status effort = %q, want low", effort)
	}
}

func TestRuntimeModelSlotValidationRejectsOpenAIGPT5ChatTools(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Providers.OpenAI.APIKey = "test-key"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	validation := rt.ValidateModelSlotConfig(core.ModelSlotConfig{
		Slot:      core.ModelSlotGovernor,
		Provider:  core.ModelProviderOpenAI,
		Model:     "gpt-5.5",
		Effort:    "high",
		Transport: core.ModelTransportOpenAIChat,
	})
	if validation.Valid {
		t.Fatal("validation.Valid = true, want false")
	}
	if validation.Error == "" {
		t.Fatal("validation error empty")
	}
}
