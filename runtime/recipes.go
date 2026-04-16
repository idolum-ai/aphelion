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
)

const (
	personaModelSonnet   = "claude-sonnet-4-6"
	personaModelOpus46   = "claude-opus-4-6"
	personaModelOpus47   = "claude-opus-4-7"
	personaEffortSonnet  = "sonnet"
	personaEffortOpus    = "opus"
	governorEffortLow    = "low"
	governorEffortMedium = "medium"
	governorEffortHigh   = "high"
	governorEffortXHigh  = "xhigh"
)

type runtimeRecipeState struct {
	PersonaModel   string `json:"persona_model"`
	PersonaEffort  string `json:"persona_effort,omitempty"` // legacy compatibility field
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
		state.PersonaEffort = personaEffortForModel(state.PersonaModel)
		return state
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(cfg.Providers.Anthropic.Model)), "opus") ||
		strings.Contains(strings.ToLower(strings.TrimSpace(cfg.Providers.OpenRouter.Model)), "opus") {
		state.PersonaModel = personaModelOpus46
	}

	defaultEffort := normalizeGovernorEffort(cfg.Thinking.Defaults.Default)
	if defaultEffort == "" {
		defaultEffort = normalizeGovernorEffort(cfg.Thinking.Effort)
	}
	if defaultEffort != "" {
		state.GovernorEffort = defaultEffort
	}
	state.PersonaEffort = personaEffortForModel(state.PersonaModel)
	return state
}

func normalizeRuntimeRecipeState(state runtimeRecipeState, cfg *config.Config) runtimeRecipeState {
	defaults := defaultRuntimeRecipeState(cfg)
	model := normalizePersonaModel(state.PersonaModel)
	if model == "" {
		model = personaModelForEffort(state.PersonaEffort)
	}
	if model == "" {
		model = defaults.PersonaModel
	}
	state.PersonaModel = model
	state.PersonaEffort = personaEffortForModel(model)
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
		model = personaModelForEffort(r.recipeState.PersonaEffort)
	}
	if model == "" {
		model = personaModelSonnet
	}
	return recipeSnapshot{
		PersonaModel:   model,
		PersonaEffort:  personaEffortForModel(model),
		GovernorEffort: r.recipeState.GovernorEffort,
	}
}

func (r *Runtime) TogglePersonaEffort() (string, error) {
	if r == nil {
		return "", fmt.Errorf("runtime is nil")
	}
	r.recipeMu.Lock()
	prev := r.recipeState
	currentModel := normalizePersonaModel(r.recipeState.PersonaModel)
	if currentModel == "" {
		currentModel = personaModelForEffort(r.recipeState.PersonaEffort)
	}
	nextModel := personaModelOpus46
	if currentModel == personaModelOpus46 || currentModel == personaModelOpus47 {
		nextModel = personaModelSonnet
	}
	r.recipeState.PersonaModel = nextModel
	r.recipeState.PersonaEffort = personaEffortForModel(nextModel)
	state := r.recipeState
	r.recipeMu.Unlock()
	if err := saveRuntimeRecipeState(r.recipePath, state, &r.recipeFileMu); err != nil {
		r.recipeMu.Lock()
		r.recipeState = prev
		r.recipeMu.Unlock()
		return "", err
	}
	return personaEffortForModel(nextModel), nil
}

func (r *Runtime) ToggleGovernorEffort() (string, error) {
	if r == nil {
		return "", fmt.Errorf("runtime is nil")
	}
	r.recipeMu.Lock()
	prev := r.recipeState
	next := governorEffortHigh
	if r.recipeState.GovernorEffort == governorEffortHigh {
		next = governorEffortMedium
	}
	r.recipeState.GovernorEffort = next
	state := r.recipeState
	r.recipeMu.Unlock()
	if err := saveRuntimeRecipeState(r.recipePath, state, &r.recipeFileMu); err != nil {
		r.recipeMu.Lock()
		r.recipeState = prev
		r.recipeMu.Unlock()
		return "", err
	}
	return next, nil
}

func (r *Runtime) CurrentEfforts() (persona string, governor string) {
	snapshot := r.currentRecipeSnapshot()
	return snapshot.PersonaEffort, snapshot.GovernorEffort
}

func (r *Runtime) CurrentPersonaModel() string {
	return r.currentRecipeSnapshot().PersonaModel
}

func (r *Runtime) PersonaModelOptions() []string {
	return []string{personaModelSonnet, personaModelOpus46, personaModelOpus47}
}

func (r *Runtime) SetPersonaModel(model string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("runtime is nil")
	}
	model = normalizePersonaModel(model)
	if model == "" {
		return "", fmt.Errorf("persona model must be one of %s", strings.Join(r.PersonaModelOptions(), ", "))
	}
	r.recipeMu.Lock()
	prev := r.recipeState
	r.recipeState.PersonaModel = model
	r.recipeState.PersonaEffort = personaEffortForModel(model)
	state := r.recipeState
	r.recipeMu.Unlock()
	if err := saveRuntimeRecipeState(r.recipePath, state, &r.recipeFileMu); err != nil {
		r.recipeMu.Lock()
		r.recipeState = prev
		r.recipeMu.Unlock()
		return "", err
	}
	return model, nil
}

func (r *Runtime) GovernorEffortOptions() []string {
	return []string{
		governorEffortLow,
		governorEffortMedium,
		governorEffortHigh,
		governorEffortXHigh,
	}
}

func (r *Runtime) SetGovernorEffort(effort string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("runtime is nil")
	}
	effort = normalizeGovernorEffort(effort)
	if effort == "" {
		return "", fmt.Errorf("governor effort must be one of %s", strings.Join(r.GovernorEffortOptions(), ", "))
	}
	r.recipeMu.Lock()
	prev := r.recipeState
	r.recipeState.GovernorEffort = effort
	state := r.recipeState
	r.recipeMu.Unlock()
	if err := saveRuntimeRecipeState(r.recipePath, state, &r.recipeFileMu); err != nil {
		r.recipeMu.Lock()
		r.recipeState = prev
		r.recipeMu.Unlock()
		return "", err
	}
	return effort, nil
}

func normalizePersonaModel(model string) string {
	value := strings.ToLower(strings.TrimSpace(model))
	value = strings.TrimPrefix(value, "anthropic/")
	switch value {
	case personaModelSonnet, personaModelOpus46, personaModelOpus47:
		return value
	default:
		return ""
	}
}

func personaModelForEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case personaEffortOpus:
		return personaModelOpus46
	case personaEffortSonnet:
		return personaModelSonnet
	default:
		return ""
	}
}

func personaEffortForModel(model string) string {
	if normalized := normalizePersonaModel(model); normalized == personaModelOpus46 || normalized == personaModelOpus47 {
		return personaEffortOpus
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
