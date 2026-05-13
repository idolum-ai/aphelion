//go:build linux

package runtime

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
)

func TestRuntimeRecipeStateRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Sessions.DBPath = filepath.Join(t.TempDir(), "state", "sessions.db")
	path := recipeStatePath(&cfg)
	want := runtimeRecipeState{
		PersonaModel:   personaModelOpus46,
		GovernorEffort: governorEffortHigh,
	}
	if err := saveRuntimeRecipeState(path, want, nil); err != nil {
		t.Fatalf("saveRuntimeRecipeState() err = %v", err)
	}
	got, err := loadRuntimeRecipeState(path, &cfg)
	if err != nil {
		t.Fatalf("loadRuntimeRecipeState() err = %v", err)
	}
	if got != want {
		t.Fatalf("recipe state = %#v, want %#v", got, want)
	}
}

func TestRuntimeReasoningOverrideAppliesOnlyInteractiveAndRecovery(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Thinking: config.ThinkingConfig{
			Effort:  "medium",
			Summary: "auto",
			Defaults: config.ThinkingDefaultsConfig{
				Default:   "medium",
				Heartbeat: "low",
				Cron:      "low",
				Recovery:  "medium",
			},
		},
	}
	rt := &Runtime{
		cfg: cfg,
		recipeState: runtimeRecipeState{
			GovernorEffort: governorEffortHigh,
		},
	}

	if got := rt.reasoningOptionsForRun(session.TurnRunKindInteractive); got.Reasoning.Effort != agent.ReasoningEffortHigh {
		t.Fatalf("interactive effort = %q, want high", got.Reasoning.Effort)
	}
	if got := rt.reasoningOptionsForRun(session.TurnRunKindRecovery); got.Reasoning.Effort != agent.ReasoningEffortHigh {
		t.Fatalf("recovery effort = %q, want high", got.Reasoning.Effort)
	}
	if got := rt.reasoningOptionsForRun(session.TurnRunKindHeartbeat); got.Reasoning.Effort != agent.ReasoningEffortLow {
		t.Fatalf("heartbeat effort = %q, want low", got.Reasoning.Effort)
	}
	if got := rt.reasoningOptionsForRun(session.TurnRunKindCron); got.Reasoning.Effort != agent.ReasoningEffortLow {
		t.Fatalf("cron effort = %q, want low", got.Reasoning.Effort)
	}
}
