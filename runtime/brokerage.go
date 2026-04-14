//go:build linux

package runtime

import (
	"context"
	"fmt"
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
	Active             bool
	Mode               string
	IdolumNote         string
	SuggestedTurnMode  string
	Ratification       string
	SignalJudgment     string
	RatificationRecord string
	RatifiedSteps      []string
	RatifiedTurnMode   string
}

func (r *Runtime) withBrokerageAwareness(aw prompt.RuntimeAwareness, brokerage turnBrokerage) prompt.RuntimeAwareness {
	aw.BrokerageActive = brokerage.Active
	aw.BrokerageMode = strings.TrimSpace(brokerage.Mode)
	aw.SuggestedTurnMode = strings.TrimSpace(brokerage.SuggestedTurnMode)
	aw.BrokerageRatification = strings.TrimSpace(brokerage.Ratification)
	aw.RatifiedTurnMode = strings.TrimSpace(brokerage.RatifiedTurnMode)
	aw.SignalJudgment = strings.TrimSpace(brokerage.SignalJudgment)
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

func normalizeRatification(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "accept", "adapt", "reject":
		return value
	default:
		return ""
	}
}

func normalizeSignalJudgment(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "", "confirmed", "overridden", "not_material":
		return value
	default:
		return ""
	}
}

func parsePlanStepLine(line string) string {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
		return strings.TrimSpace(line[2:])
	}
	dot := strings.Index(line, ". ")
	if dot <= 0 {
		return ""
	}
	for _, ch := range line[:dot] {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	return strings.TrimSpace(line[dot+2:])
}

func parseBrokerageRatification(text string) (turnBrokerage, error) {
	parsed := turnBrokerage{RatificationRecord: strings.TrimSpace(text)}
	if parsed.RatificationRecord == "" {
		return parsed, fmt.Errorf("empty brokerage ratification")
	}

	inPlan := false
	for _, rawLine := range strings.Split(parsed.RatificationRecord, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "MODE:"):
			parsed.RatifiedTurnMode = normalizeTurnMode(strings.TrimSpace(line[len("MODE:"):]))
			inPlan = false
			continue
		case strings.HasPrefix(upper, "RATIFICATION:"):
			parsed.Ratification = normalizeRatification(strings.TrimSpace(line[len("RATIFICATION:"):]))
			inPlan = false
			continue
		case strings.HasPrefix(upper, "SIGNAL_JUDGMENT:"):
			parsed.SignalJudgment = normalizeSignalJudgment(strings.TrimSpace(line[len("SIGNAL_JUDGMENT:"):]))
			inPlan = false
			continue
		case upper == "PLAN:":
			inPlan = true
			continue
		}
		if !inPlan {
			continue
		}
		if step := parsePlanStepLine(line); step != "" {
			parsed.RatifiedSteps = append(parsed.RatifiedSteps, step)
		}
	}

	switch {
	case parsed.RatifiedTurnMode == "":
		return parsed, fmt.Errorf("missing ratified turn mode")
	case parsed.Ratification == "":
		return parsed, fmt.Errorf("missing ratification disposition")
	case len(parsed.RatifiedSteps) == 0:
		return parsed, fmt.Errorf("missing ratified execution steps")
	default:
		return parsed, nil
	}
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
			"SIGNAL_JUDGMENT: <confirmed|overridden|not_material>  # optional; use when Idolum named a hidden input",
			"PLAN:",
			"- <short concrete first step>",
			"- <optional second step>",
			"- <optional third step>",
			"",
			"User message:",
			strings.TrimSpace(userText),
		}, "\n"),
	})

	resp, err := completeProvider(ctx, exec.Provider, messages, nil, r.reasoningOptionsForRun(session.TurnRunKindInteractive))
	if err != nil {
		return brokerage, core.TokenUsage{}, err
	}

	parsed, parseErr := parseBrokerageRatification(resp.Content)
	if parseErr != nil {
		return brokerage, resp.Usage, fmt.Errorf("parse brokerage ratification: %w", parseErr)
	}
	brokerage.RatificationRecord = parsed.RatificationRecord
	brokerage.Ratification = parsed.Ratification
	brokerage.SignalJudgment = parsed.SignalJudgment
	brokerage.RatifiedTurnMode = parsed.RatifiedTurnMode
	brokerage.RatifiedSteps = append(brokerage.RatifiedSteps[:0], parsed.RatifiedSteps...)
	return brokerage, resp.Usage, nil
}

func brokerageContextForGovernor(brokerage turnBrokerage) string {
	if brokerage.Active && brokerage.Mode == "brokerage" && strings.TrimSpace(brokerage.RatificationRecord) != "" {
		if block := prompt.RenderBrokeragePlanForGovernor(prompt.BrokerageArtifact{
			IdolumProposal:     brokerage.IdolumNote,
			RatifiedTurnMode:   brokerage.RatifiedTurnMode,
			Ratification:       brokerage.Ratification,
			SignalJudgment:     brokerage.SignalJudgment,
			RatifiedSteps:      brokerage.RatifiedSteps,
			RatificationRecord: brokerage.RatificationRecord,
		}); block != "" {
			return block
		}
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

func maybeSeedPlanFromBrokerage(current session.PlanState, brokerage turnBrokerage) session.PlanState {
	current = session.NormalizePlanState(current)
	if len(current.Steps) > 0 || len(brokerage.RatifiedSteps) == 0 {
		return current
	}
	steps := make([]session.PlanStep, 0, len(brokerage.RatifiedSteps))
	for i, step := range brokerage.RatifiedSteps {
		status := session.PlanStatusPending
		if i == 0 {
			status = session.PlanStatusInProgress
		}
		steps = append(steps, session.PlanStep{
			Step:   strings.TrimSpace(step),
			Status: status,
		})
	}
	explanation := "Ratified execution plan."
	if mode := strings.TrimSpace(brokerage.RatifiedTurnMode); mode != "" {
		explanation = fmt.Sprintf("Ratified %s plan.", mode)
	}
	return session.NormalizePlanState(session.PlanState{
		Explanation: explanation,
		Steps:       steps,
	})
}
