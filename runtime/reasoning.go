//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	interactiveRunMaxTokens = 2048
	heartbeatRunMaxTokens   = 1024
	cronRunMaxTokens        = 1024
	recoveryRunMaxTokens    = 1024
	doctorRunMaxTokens      = 4096
	faceRenderMaxTokens     = 512
	compactionMaxTokens     = 512
)

func reasoningOptionsForRun(cfg *config.Config, kind session.TurnRunKind) *agent.CompleteOptions {
	if cfg == nil {
		return nil
	}

	effort := strings.ToLower(strings.TrimSpace(cfg.Thinking.Effort))
	switch kind {
	case session.TurnRunKindHeartbeat:
		effort = firstNonEmptyThinking(cfg.Thinking.Defaults.Heartbeat, effort)
	case session.TurnRunKindCron:
		effort = firstNonEmptyThinking(cfg.Thinking.Defaults.Cron, effort)
	case session.TurnRunKindRecovery, session.TurnRunKindDoctor:
		effort = firstNonEmptyThinking(cfg.Thinking.Defaults.Recovery, effort)
	default:
		effort = firstNonEmptyThinking(cfg.Thinking.Defaults.Default, effort)
	}
	if effort == "" {
		effort = string(agent.ReasoningEffortMedium)
	}

	summary := strings.ToLower(strings.TrimSpace(cfg.Thinking.Summary))
	if summary == "" {
		summary = string(agent.ReasoningSummaryAuto)
	}

	return &agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{
			Effort:  agent.ReasoningEffort(effort),
			Summary: agent.ReasoningSummaryMode(summary),
		},
		MaxTokens: maxTokensForRunKind(kind),
	}
}

func maxTokensForRunKind(kind session.TurnRunKind) int {
	switch kind {
	case session.TurnRunKindDoctor:
		return doctorRunMaxTokens
	case session.TurnRunKindHeartbeat:
		return heartbeatRunMaxTokens
	case session.TurnRunKindCron:
		return cronRunMaxTokens
	case session.TurnRunKindRecovery:
		return recoveryRunMaxTokens
	default:
		return interactiveRunMaxTokens
	}
}

func (r *Runtime) reasoningOptionsForRun(kind session.TurnRunKind) *agent.CompleteOptions {
	opts := reasoningOptionsForRun(r.cfg, kind)
	if opts == nil {
		return nil
	}
	snapshot := r.currentRecipeSnapshot()
	if kind == session.TurnRunKindInteractive || kind == session.TurnRunKindRecovery || kind == session.TurnRunKindDoctor {
		if effort := normalizeGovernorEffort(snapshot.GovernorEffort); effort != "" {
			opts.Reasoning.Effort = agent.ReasoningEffort(effort)
		}
		slot := core.ModelSlotGovernor
		if kind == session.TurnRunKindDoctor {
			slot = core.ModelSlotDoctor
		}
		if status, err := r.EffectiveModelSlot(slot); err == nil && status.Validation.Valid {
			if effort := core.NormalizeModelEffort(status.Effective.Effort); effort != "" {
				opts.Reasoning.Effort = agent.ReasoningEffort(effort)
			}
		}
	}
	return opts
}

func firstNonEmptyThinking(values ...string) string {
	for _, value := range values {
		if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
