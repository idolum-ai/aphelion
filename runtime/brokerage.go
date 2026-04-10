//go:build linux

package runtime

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const (
	turnModeAnswerNow        = "answer_now"
	turnModeInspectThenReply = "inspect_then_answer"
	turnModeAskThenWait      = "ask_then_wait"
	turnModeDecline          = "decline"
	turnModeSilent           = "silent"
)

type turnBrokerage struct {
	Active            bool
	Mode              string
	IdolumNote        string
	SuggestedTurnMode string
	RatifiedPlan      string
	RatifiedTurnMode  string
}

func (r *Runtime) withBrokerageAwareness(aw prompt.RuntimeAwareness, brokerage turnBrokerage) prompt.RuntimeAwareness {
	aw.BrokerageActive = brokerage.Active
	aw.BrokerageMode = strings.TrimSpace(brokerage.Mode)
	aw.SuggestedTurnMode = strings.TrimSpace(brokerage.SuggestedTurnMode)
	aw.RatifiedTurnMode = strings.TrimSpace(brokerage.RatifiedTurnMode)
	return aw
}

func normalizeTurnMode(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case turnModeAnswerNow, turnModeInspectThenReply, turnModeAskThenWait, turnModeDecline, turnModeSilent:
		return value
	default:
		return ""
	}
}

func parseBrokerageMode(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "MODE:") {
			continue
		}
		return normalizeTurnMode(strings.TrimSpace(line[len("MODE:"):]))
	}
	return ""
}

func (r *Runtime) ratifyTurnBrokerage(
	ctx context.Context,
	exec governorExecution,
	systemBlocks []agent.SystemBlock,
	history []agent.Message,
	userText string,
	brokerage turnBrokerage,
) (turnBrokerage, core.TokenUsage, error) {
	if strings.TrimSpace(brokerage.IdolumNote) == "" {
		return brokerage, core.TokenUsage{}, nil
	}

	messages := make([]agent.Message, 0, len(history)+3)
	messages = append(messages, agent.Message{
		Role:         "system",
		Content:      prompt.RenderSystemBlocks(systemBlocks),
		SystemBlocks: systemBlocks,
	})
	if advisory := prompt.RenderIdolumBrokerageForGovernor("Idolum", brokerage.IdolumNote); advisory != "" {
		messages = append(messages, agent.Message{Role: "system", Content: advisory})
	}
	messages = append(messages, history...)
	messages = append(messages, agent.Message{
		Role: "user",
		Content: strings.Join([]string{
			"The latest user message is below.",
			"Before the main turn executes, ratify how this turn should proceed.",
			"Return exactly this structure and nothing else:",
			"MODE: <answer_now|inspect_then_answer|ask_then_wait|decline|silent>",
			"RATIFICATION: <accept|adapt|reject>",
			"PLAN:",
			"- <short concrete step>",
			"- <optional second step>",
			"",
			"User message:",
			strings.TrimSpace(userText),
		}, "\n"),
	})

	resp, err := completeProvider(ctx, exec.Provider, messages, nil, r.reasoningOptionsForRun(session.TurnRunKindInteractive))
	if err != nil {
		return brokerage, core.TokenUsage{}, err
	}

	brokerage.RatifiedPlan = strings.TrimSpace(resp.Content)
	if mode := parseBrokerageMode(resp.Content); mode != "" {
		brokerage.RatifiedTurnMode = mode
	}
	return brokerage, resp.Usage, nil
}

func brokerageContextForGovernor(brokerage turnBrokerage) string {
	if plan := prompt.RenderBrokeragePlanForGovernor(brokerage.RatifiedPlan); plan != "" {
		return plan
	}
	if brokerage.Active && brokerage.Mode == "brokerage" {
		return prompt.RenderIdolumBrokerageForGovernor("Idolum", brokerage.IdolumNote)
	}
	return prompt.RenderIdolumProposalForGovernor("Idolum", brokerage.IdolumNote)
}

func brokerageModeName(active bool, mode string) string {
	if !active {
		return ""
	}
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" {
		return "proposal"
	}
	return trimmed
}
