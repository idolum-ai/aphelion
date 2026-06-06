//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

func (r *Runtime) renderLaneProvider() (agent.Provider, core.ModelSlotStatus, bool) {
	if r == nil {
		return nil, core.ModelSlotStatus{}, false
	}
	status, err := r.EffectiveModelSlot(core.ModelSlotPersona)
	if err != nil || !status.Validation.Valid {
		return nil, status, false
	}
	provider, err := r.cachedProviderForModelSlot(status.Effective)
	if err != nil {
		return nil, status, false
	}
	return provider, status, provider != nil
}

func renderLaneCompleteOptions(status core.ModelSlotStatus, maxTokens int) *agent.CompleteOptions {
	opts := &agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{
			Effort:  agent.ReasoningEffortLow,
			Summary: agent.ReasoningSummaryAuto,
		},
		MaxTokens: maxTokens,
	}
	if effort := core.NormalizeModelEffort(status.Effective.Effort); effort != "" {
		opts.Reasoning.Effort = agent.ReasoningEffort(effort)
	}
	return opts
}
