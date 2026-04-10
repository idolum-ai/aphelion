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
	personaEffortSonnet  = "sonnet"
	personaEffortOpus    = "opus"
	governorEffortMedium = "medium"
	governorEffortHigh   = "high"
)

type runtimeRecipeState struct {
	PersonaEffort  string `json:"persona_effort"`
	GovernorEffort string `json:"governor_effort"`
}

type recipeSnapshot struct {
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
		PersonaEffort:  personaEffortSonnet,
		GovernorEffort: governorEffortMedium,
	}
	if cfg == nil {
		return state
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(cfg.Providers.Anthropic.Model)), "opus") ||
		strings.Contains(strings.ToLower(strings.TrimSpace(cfg.Providers.OpenRouter.Model)), "opus") {
		state.PersonaEffort = personaEffortOpus
	}

	defaultEffort := strings.ToLower(strings.TrimSpace(cfg.Thinking.Defaults.Default))
	if defaultEffort == "" {
		defaultEffort = strings.ToLower(strings.TrimSpace(cfg.Thinking.Effort))
	}
	switch defaultEffort {
	case string(governorEffortHigh), "xhigh":
		state.GovernorEffort = governorEffortHigh
	default:
		state.GovernorEffort = governorEffortMedium
	}
	return state
}

func normalizeRuntimeRecipeState(state runtimeRecipeState, cfg *config.Config) runtimeRecipeState {
	defaults := defaultRuntimeRecipeState(cfg)
	switch strings.ToLower(strings.TrimSpace(state.PersonaEffort)) {
	case personaEffortSonnet, personaEffortOpus:
		state.PersonaEffort = strings.ToLower(strings.TrimSpace(state.PersonaEffort))
	default:
		state.PersonaEffort = defaults.PersonaEffort
	}
	switch strings.ToLower(strings.TrimSpace(state.GovernorEffort)) {
	case governorEffortMedium, governorEffortHigh:
		state.GovernorEffort = strings.ToLower(strings.TrimSpace(state.GovernorEffort))
	default:
		state.GovernorEffort = defaults.GovernorEffort
	}
	return state
}

func loadRuntimeRecipeState(path string, cfg *config.Config) (runtimeRecipeState, error) {
	state := defaultRuntimeRecipeState(cfg)
	if strings.TrimSpace(path) == "" {
		return state, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, fmt.Errorf("read runtime recipe state: %w", err)
	}
	if len(data) == 0 {
		return state, nil
	}
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
			PersonaEffort:  personaEffortSonnet,
			GovernorEffort: governorEffortMedium,
		}
	}
	r.recipeMu.Lock()
	defer r.recipeMu.Unlock()
	return recipeSnapshot{
		PersonaEffort:  r.recipeState.PersonaEffort,
		GovernorEffort: r.recipeState.GovernorEffort,
	}
}

func (r *Runtime) TogglePersonaEffort() (string, error) {
	if r == nil {
		return "", fmt.Errorf("runtime is nil")
	}
	r.recipeMu.Lock()
	prev := r.recipeState
	next := personaEffortOpus
	if r.recipeState.PersonaEffort == personaEffortOpus {
		next = personaEffortSonnet
	}
	r.recipeState.PersonaEffort = next
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
