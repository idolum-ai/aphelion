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
	// Legacy mode names remain as a compatibility parser for older proposals and
	// ratification records.
	turnModeAnswerNow        = "answer_now"
	turnModeInspectThenReply = "inspect_then_answer"
	turnModeAskThenWait      = "ask_then_wait"
	turnModeDecline          = "decline"
	turnModeSilent           = "silent"
	maxBrokerageRounds       = 3
)

type executionContract struct {
	NeedsInspection bool
	NeedsQuestion   bool
	MayAnswerNow    bool
}

func (c executionContract) Summary() string {
	return fmt.Sprintf("inspect=%s, question=%s, answer=%s", yesNo(c.NeedsInspection), yesNo(c.NeedsQuestion), yesNo(c.MayAnswerNow))
}

type turnBrokerage struct {
	Active                     bool
	Phase                      string
	IdolumNote                 string
	SuggestedExecutionContract *executionContract
	Ratification               string
	SignalJudgment             string
	RatificationRecord         string
	RatifiedSteps              []string
	RatifiedExecutionContract  *executionContract
}

func (r *Runtime) withBrokerageAwareness(aw prompt.RuntimeAwareness, brokerage turnBrokerage) prompt.RuntimeAwareness {
	aw.BrokerageActive = brokerage.Active
	aw.BrokeragePhase = strings.TrimSpace(brokerage.Phase)
	aw.SuggestedExecutionContract = summarizeExecutionContract(brokerage.SuggestedExecutionContract)
	aw.BrokerageRatification = strings.TrimSpace(brokerage.Ratification)
	aw.RatifiedExecutionContract = summarizeExecutionContract(brokerage.RatifiedExecutionContract)
	aw.SignalJudgment = strings.TrimSpace(brokerage.SignalJudgment)
	return aw
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func summarizeExecutionContract(contract *executionContract) string {
	if contract == nil {
		return ""
	}
	return contract.Summary()
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

func executionContractFromTurnMode(raw string) (*executionContract, bool) {
	switch normalizeTurnMode(raw) {
	case turnModeAnswerNow, turnModeDecline:
		return &executionContract{MayAnswerNow: true}, true
	case turnModeInspectThenReply:
		return &executionContract{NeedsInspection: true, MayAnswerNow: true}, true
	case turnModeAskThenWait:
		return &executionContract{NeedsQuestion: true}, true
	case turnModeSilent:
		return &executionContract{}, true
	default:
		return nil, false
	}
}

func normalizeDirectiveBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "true", "required", "needed", "y":
		return true, true
	case "no", "false", "not_required", "not needed", "n":
		return false, true
	default:
		return false, false
	}
}

func parseProposalExecutionContract(text string) *executionContract {
	contract := executionContract{}
	inspectSet := false
	questionSet := false
	answerSet := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "INSPECT:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("INSPECT:"):])); ok {
				contract.NeedsInspection = value
				inspectSet = true
			}
		case strings.HasPrefix(upper, "QUESTION:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("QUESTION:"):])); ok {
				contract.NeedsQuestion = value
				questionSet = true
			}
		case strings.HasPrefix(upper, "ANSWER:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("ANSWER:"):])); ok {
				contract.MayAnswerNow = value
				answerSet = true
			}
		case strings.HasPrefix(upper, "MODE:"):
			if legacy, ok := executionContractFromTurnMode(strings.TrimSpace(line[len("MODE:"):])); ok {
				return legacy
			}
		}
	}
	if inspectSet && questionSet && answerSet {
		return &contract
	}
	return nil
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

	contract := executionContract{}
	inspectSet := false
	questionSet := false
	answerSet := false
	inPlan := false
	for _, rawLine := range strings.Split(parsed.RatificationRecord, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "INSPECT:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("INSPECT:"):])); ok {
				contract.NeedsInspection = value
				inspectSet = true
			}
			inPlan = false
			continue
		case strings.HasPrefix(upper, "QUESTION:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("QUESTION:"):])); ok {
				contract.NeedsQuestion = value
				questionSet = true
			}
			inPlan = false
			continue
		case strings.HasPrefix(upper, "ANSWER:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("ANSWER:"):])); ok {
				contract.MayAnswerNow = value
				answerSet = true
			}
			inPlan = false
			continue
		case strings.HasPrefix(upper, "MODE:"):
			if legacy, ok := executionContractFromTurnMode(strings.TrimSpace(line[len("MODE:"):])); ok {
				contract = *legacy
				inspectSet = true
				questionSet = true
				answerSet = true
			}
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
	if inspectSet && questionSet && answerSet {
		parsed.RatifiedExecutionContract = &contract
	}

	switch {
	case parsed.RatifiedExecutionContract == nil:
		return parsed, fmt.Errorf("missing ratified execution contract")
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
			"INSPECT: <yes|no>",
			"QUESTION: <yes|no>",
			"ANSWER: <yes|no>",
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
	brokerage.RatifiedExecutionContract = parsed.RatifiedExecutionContract
	brokerage.RatifiedSteps = append(brokerage.RatifiedSteps[:0], parsed.RatifiedSteps...)
	return brokerage, resp.Usage, nil
}

func brokerageContextForGovernor(brokerage turnBrokerage) string {
	if brokerage.Active && brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.RatificationRecord) != "" {
		if block := prompt.RenderBrokeragePlanForGovernor(prompt.BrokerageArtifact{
			IdolumProposal:            brokerage.IdolumNote,
			RatifiedExecutionContract: summarizeExecutionContract(brokerage.RatifiedExecutionContract),
			Ratification:              brokerage.Ratification,
			SignalJudgment:            brokerage.SignalJudgment,
			RatifiedSteps:             brokerage.RatifiedSteps,
			RatificationRecord:        brokerage.RatificationRecord,
		}); block != "" {
			return block
		}
	}
	if brokerage.Active && brokerage.Phase == "brokerage" {
		return prompt.RenderIdolumBrokerageForGovernor("Idolum", brokerage.IdolumNote)
	}
	return prompt.RenderIdolumProposalForGovernor("Idolum", brokerage.IdolumNote)
}

func brokeragePhaseName(active bool, phase string) string {
	if !active {
		return ""
	}
	trimmed := strings.TrimSpace(phase)
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
	return session.NormalizePlanState(session.PlanState{
		Explanation: "Ratified execution plan.",
		Steps:       steps,
	})
}

type brokerageFaceRequester func(mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error)

func (r *Runtime) convergeTurnBrokerage(
	ctx context.Context,
	exec governorExecution,
	baseAwareness prompt.RuntimeAwareness,
	systemBlocks []agent.SystemBlock,
	history []agent.Message,
	userText string,
	brokerage turnBrokerage,
	requestFaceNote brokerageFaceRequester,
	audit *turnAuditRecorder,
) (turnBrokerage, core.TokenUsage) {
	if strings.TrimSpace(brokerage.IdolumNote) == "" || brokerage.Phase != "brokerage" {
		return brokerage, core.TokenUsage{}
	}

	totalUsage := core.TokenUsage{}
	for round := 1; round <= maxBrokerageRounds; round++ {
		updated, usage, ratifyErr := r.ratifyTurnBrokerage(ctx, exec, systemBlocks, history, userText, brokerage)
		totalUsage = addTokenUsage(totalUsage, usage)
		roundAudit := BrokerageRoundAudit{
			Round:                      round,
			Phase:                      brokerage.Phase,
			IdolumNote:                 strings.TrimSpace(brokerage.IdolumNote),
			SuggestedExecutionContract: summarizeExecutionContract(brokerage.SuggestedExecutionContract),
		}
		if ratifyErr != nil {
			roundAudit.Error = ratifyErr.Error()
			if audit != nil {
				audit.RecordBrokerageRound(roundAudit)
				audit.MarkBrokerageConverged(false)
			}
			return r.fallbackToPlainProposal(ctx, baseAwareness, brokerage, requestFaceNote, totalUsage)
		}

		brokerage = updated
		roundAudit.Ratification = strings.TrimSpace(brokerage.Ratification)
		roundAudit.RatifiedExecutionContract = summarizeExecutionContract(brokerage.RatifiedExecutionContract)
		roundAudit.SignalJudgment = strings.TrimSpace(brokerage.SignalJudgment)
		roundAudit.RatifiedSteps = append([]string(nil), brokerage.RatifiedSteps...)
		if audit != nil {
			audit.RecordBrokerageRound(roundAudit)
		}
		if brokerage.Ratification == "accept" {
			if audit != nil {
				audit.MarkBrokerageConverged(true)
			}
			return brokerage, totalUsage
		}
		if round == maxBrokerageRounds {
			if audit != nil {
				audit.MarkBrokerageConverged(false)
			}
			return r.fallbackToPlainProposal(ctx, baseAwareness, brokerage, requestFaceNote, totalUsage)
		}

		reviseAwareness := r.withBrokerageAwareness(baseAwareness, brokerage)
		reviseAwareness.ArtifactMode = "scene"
		revised, proposalUsage, proposalErr := requestFaceNote("brokerage", reviseAwareness, brokerage.IdolumNote, brokerage.RatificationRecord)
		totalUsage = addTokenUsage(totalUsage, proposalUsage)
		if proposalErr != nil || strings.TrimSpace(revised) == "" {
			if audit != nil {
				audit.MarkBrokerageConverged(false)
			}
			return r.fallbackToPlainProposal(ctx, baseAwareness, brokerage, requestFaceNote, totalUsage)
		}
		brokerage.IdolumNote = strings.TrimSpace(revised)
		brokerage.SuggestedExecutionContract = parseProposalExecutionContract(revised)
		brokerage.Ratification = ""
		brokerage.SignalJudgment = ""
		brokerage.RatificationRecord = ""
		brokerage.RatifiedSteps = nil
		brokerage.RatifiedExecutionContract = nil
	}
	if audit != nil {
		audit.MarkBrokerageConverged(false)
	}
	return brokerage, totalUsage
}

func (r *Runtime) fallbackToPlainProposal(
	ctx context.Context,
	baseAwareness prompt.RuntimeAwareness,
	brokerage turnBrokerage,
	requestFaceNote brokerageFaceRequester,
	currentUsage core.TokenUsage,
) (turnBrokerage, core.TokenUsage) {
	brokerage.Ratification = ""
	brokerage.SignalJudgment = ""
	brokerage.RatificationRecord = ""
	brokerage.RatifiedSteps = nil
	brokerage.RatifiedExecutionContract = nil
	brokerage.SuggestedExecutionContract = nil

	proposal, proposalUsage, proposalErr := requestFaceNote("proposal", baseAwareness, "", "")
	currentUsage = addTokenUsage(currentUsage, proposalUsage)
	if proposalErr == nil {
		brokerage.IdolumNote = strings.TrimSpace(proposal)
	}
	brokerage.Active = strings.TrimSpace(brokerage.IdolumNote) != ""
	brokerage.Phase = brokeragePhaseName(brokerage.Active, "proposal")
	return brokerage, currentUsage
}
