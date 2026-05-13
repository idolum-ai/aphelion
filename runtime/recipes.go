//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

const (
	personaModelSonnet   = "claude-sonnet-4-6"
	personaModelOpus46   = "claude-opus-4-6"
	personaModelOpus47   = "claude-opus-4-7"
	personaModelGPT55    = "gpt-5.5"
	personaModelOpus     = personaModelOpus47
	personaEffortSonnet  = "sonnet"
	personaEffortOpus    = "opus"
	personaEffortGPT     = "gpt"
	governorEffortLow    = "low"
	governorEffortMedium = "medium"
	governorEffortHigh   = "high"
	governorEffortXHigh  = "xhigh"
)

type runtimeRecipeState struct {
	PersonaModel   string `json:"persona_model"`
	GovernorEffort string `json:"governor_effort"`
}

type recipeSnapshot struct {
	PersonaModel   string
	PersonaEffort  string
	GovernorEffort string
}

func recipeStatePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	base := filepath.Dir(strings.TrimSpace(cfg.Sessions.DBPath))
	if base == "" {
		return ""
	}
	return filepath.Join(base, "runtime_recipes.json")
}

func defaultRuntimeRecipeState(cfg *config.Config) runtimeRecipeState {
	state := runtimeRecipeState{
		PersonaModel:   personaModelSonnet,
		GovernorEffort: governorEffortMedium,
	}
	if cfg == nil {
		return state
	}
	nativeProvider := config.EffectiveNativeProvider(cfg)
	if nativeProvider == "openai" ||
		(strings.TrimSpace(cfg.Providers.OpenAI.APIKey) != "" && strings.EqualFold(strings.TrimSpace(cfg.Providers.OpenAI.Model), personaModelGPT55)) {
		state.PersonaModel = personaModelGPT55
	} else if strings.Contains(strings.ToLower(strings.TrimSpace(cfg.Providers.Anthropic.Model)), "opus") ||
		strings.Contains(strings.ToLower(strings.TrimSpace(cfg.Providers.OpenRouter.Model)), "opus") {
		state.PersonaModel = personaModelOpus
	}

	defaultEffort := normalizeGovernorEffort(cfg.Thinking.Defaults.Default)
	if defaultEffort == "" {
		defaultEffort = normalizeGovernorEffort(cfg.Thinking.Effort)
	}
	if defaultEffort != "" {
		state.GovernorEffort = defaultEffort
	}
	return state
}

func normalizeRuntimeRecipeState(state runtimeRecipeState, cfg *config.Config) runtimeRecipeState {
	defaults := defaultRuntimeRecipeState(cfg)
	model := normalizePersonaModel(state.PersonaModel)
	if model == "" {
		model = defaults.PersonaModel
	}
	state.PersonaModel = model
	effort := normalizeGovernorEffort(state.GovernorEffort)
	if effort == "" {
		effort = defaults.GovernorEffort
	}
	state.GovernorEffort = effort
	return state
}

func loadRuntimeRecipeState(path string, cfg *config.Config) (runtimeRecipeState, error) {
	defaults := defaultRuntimeRecipeState(cfg)
	if strings.TrimSpace(path) == "" {
		return defaults, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaults, nil
		}
		return defaults, fmt.Errorf("read runtime recipe state: %w", err)
	}
	if len(data) == 0 {
		return defaults, nil
	}
	var state runtimeRecipeState
	if err := json.Unmarshal(data, &state); err != nil {
		return defaultRuntimeRecipeState(cfg), fmt.Errorf("decode runtime recipe state: %w", err)
	}
	return normalizeRuntimeRecipeState(state, cfg), nil
}

func saveRuntimeRecipeState(path string, state runtimeRecipeState, mu *sync.Mutex) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create runtime recipe state directory: %w", err)
	}
	state = normalizeRuntimeRecipeState(state, nil)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime recipe state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write runtime recipe state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit runtime recipe state: %w", err)
	}
	return nil
}

func (r *Runtime) currentRecipeSnapshot() recipeSnapshot {
	if r == nil {
		return recipeSnapshot{
			PersonaModel:   personaModelSonnet,
			PersonaEffort:  personaEffortSonnet,
			GovernorEffort: governorEffortMedium,
		}
	}
	r.recipeMu.Lock()
	defer r.recipeMu.Unlock()
	model := normalizePersonaModel(r.recipeState.PersonaModel)
	if model == "" {
		model = personaModelSonnet
	}
	return recipeSnapshot{
		PersonaModel:   model,
		PersonaEffort:  personaEffortForModel(model),
		GovernorEffort: r.recipeState.GovernorEffort,
	}
}

func (r *Runtime) CurrentEfforts() (persona string, governor string) {
	snapshot := r.currentRecipeSnapshot()
	persona = snapshot.PersonaEffort
	governor = snapshot.GovernorEffort
	if status, err := r.EffectiveModelSlot(core.ModelSlotPersona); err == nil && status.Source == "override" {
		if effort := core.NormalizeModelEffort(status.Effective.Effort); effort != "" {
			persona = effort
		} else if status.Effective.Provider != "" {
			persona = status.Effective.Provider
		}
	}
	if status, err := r.EffectiveModelSlot(core.ModelSlotGovernor); err == nil && status.Source == "override" {
		if effort := core.NormalizeModelEffort(status.Effective.Effort); effort != "" {
			governor = effort
		}
	}
	return persona, governor
}

func normalizePersonaModel(model string) string {
	value := strings.ToLower(strings.TrimSpace(model))
	value = strings.TrimPrefix(value, "anthropic/")
	value = strings.TrimPrefix(value, "openai/")
	switch value {
	case personaModelSonnet, personaModelOpus46, personaModelOpus47, personaModelGPT55:
		return value
	default:
		return ""
	}
}

func personaEffortForModel(model string) string {
	if normalized := normalizePersonaModel(model); normalized == personaModelOpus46 || normalized == personaModelOpus47 {
		return personaEffortOpus
	} else if normalized == personaModelGPT55 {
		return personaEffortGPT
	}
	return personaEffortSonnet
}

func normalizeGovernorEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case governorEffortLow, governorEffortMedium, governorEffortHigh, governorEffortXHigh:
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}
