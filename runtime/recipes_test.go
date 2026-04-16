//go:build linux

package runtime

import (
	"os"
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
		PersonaModel:   personaModelOpus,
		PersonaEffort:  personaEffortOpus,
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

func TestRuntimeRecipeStateMigratesLegacyPersonaEffort(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Sessions.DBPath = filepath.Join(t.TempDir(), "state", "sessions.db")
	path := recipeStatePath(&cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() err = %v", err)
	}
	raw := []byte("{\"persona_effort\":\"opus\",\"governor_effort\":\"medium\"}\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}
	got, err := loadRuntimeRecipeState(path, &cfg)
	if err != nil {
		t.Fatalf("loadRuntimeRecipeState() err = %v", err)
	}
	if got.PersonaModel != personaModelOpus {
		t.Fatalf("PersonaModel = %q, want %q", got.PersonaModel, personaModelOpus)
	}
	if got.PersonaEffort != personaEffortOpus {
		t.Fatalf("PersonaEffort = %q, want %q", got.PersonaEffort, personaEffortOpus)
	}
}

func TestSetPersonaModelPersistsSelection(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Sessions.DBPath = filepath.Join(t.TempDir(), "state", "sessions.db")
	path := recipeStatePath(&cfg)
	rt := &Runtime{
		recipePath: path,
		recipeState: runtimeRecipeState{
			PersonaModel:   personaModelSonnet,
			PersonaEffort:  personaEffortSonnet,
			GovernorEffort: governorEffortMedium,
		},
	}
	got, err := rt.SetPersonaModel(personaModelOpus)
	if err != nil {
		t.Fatalf("SetPersonaModel() err = %v", err)
	}
	if got != personaModelOpus {
		t.Fatalf("SetPersonaModel() = %q, want %q", got, personaModelOpus)
	}
	reloaded, err := loadRuntimeRecipeState(path, &cfg)
	if err != nil {
		t.Fatalf("loadRuntimeRecipeState() err = %v", err)
	}
	if reloaded.PersonaModel != personaModelOpus {
		t.Fatalf("PersonaModel = %q, want %q", reloaded.PersonaModel, personaModelOpus)
	}
	if reloaded.PersonaEffort != personaEffortOpus {
		t.Fatalf("PersonaEffort = %q, want %q", reloaded.PersonaEffort, personaEffortOpus)
	}
}

func TestSetGovernorEffortValidatesAndPersists(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Sessions.DBPath = filepath.Join(t.TempDir(), "state", "sessions.db")
	path := recipeStatePath(&cfg)
	rt := &Runtime{
		recipePath: path,
		recipeState: runtimeRecipeState{
			PersonaModel:   personaModelSonnet,
			PersonaEffort:  personaEffortSonnet,
			GovernorEffort: governorEffortMedium,
		},
	}
	if _, err := rt.SetGovernorEffort("invalid"); err == nil {
		t.Fatal("SetGovernorEffort() err = nil, want validation error")
	}
	got, err := rt.SetGovernorEffort(governorEffortXHigh)
	if err != nil {
		t.Fatalf("SetGovernorEffort() err = %v", err)
	}
	if got != governorEffortXHigh {
		t.Fatalf("SetGovernorEffort() = %q, want %q", got, governorEffortXHigh)
	}
	reloaded, err := loadRuntimeRecipeState(path, &cfg)
	if err != nil {
		t.Fatalf("loadRuntimeRecipeState() err = %v", err)
	}
	if reloaded.GovernorEffort != governorEffortXHigh {
		t.Fatalf("GovernorEffort = %q, want %q", reloaded.GovernorEffort, governorEffortXHigh)
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
			PersonaEffort:  personaEffortSonnet,
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
