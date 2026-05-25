//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/agent"
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
		Model:     "claude-opus-4.7",
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
	if exec.ProviderName != core.ModelProviderAnthropic || exec.ModelName != "claude-opus-4.7" {
		t.Fatalf("provider/model = %s/%s, want anthropic/claude-opus-4.7", exec.ProviderName, exec.ModelName)
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
		Model:     "claude-opus-4.7",
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
