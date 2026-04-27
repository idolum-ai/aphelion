//go:build linux

package runtime

import (
	"testing"
	"time"

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
	}, "test", "route governor", time.Hour)
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
