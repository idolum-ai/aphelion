//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const (
	maxBrokerageRounds = 3
)

type executionContract struct {
	NeedsInspection bool
	NeedsQuestion   bool
	MayAnswerNow    bool
}

func (c executionContract) Summary() string {
	return fmt.Sprintf("inspect=%s, question=%s, answer=%s", yesNo(c.NeedsInspection), yesNo(c.NeedsQuestion), yesNo(c.MayAnswerNow))
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
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

func summarizeExecutionContract(contract *executionContract) string {
	if contract == nil {
		return ""
	}
	return contract.Summary()
}

func parseProposalExecutionContract(text string) *executionContract {
	parsed := pipeline.ParseExecutionContract(text)
	if parsed == nil {
		return nil
	}
	return &executionContract{
		NeedsInspection: parsed.NeedsInspection,
		NeedsQuestion:   parsed.NeedsQuestion,
		MayAnswerNow:    parsed.MayAnswerNow,
	}
}

func parseBrokerageRatification(text string) (turnBrokerage, error) {
	parsed, err := pipeline.ParseBrokerageRatification(text)
	if err != nil {
		return turnBrokerage{}, err
	}
	copy := executionContract{
		NeedsInspection: parsed.RatifiedContract.NeedsInspection,
		NeedsQuestion:   parsed.RatifiedContract.NeedsQuestion,
		MayAnswerNow:    parsed.RatifiedContract.MayAnswerNow,
	}
	return turnBrokerage{
		RatificationRecord:        parsed.RawText,
		Ratification:              string(parsed.Disposition),
		SignalJudgment:            string(parsed.SignalJudgment),
		RatifiedExecutionContract: &copy,
		RatifiedSteps:             append([]string(nil), parsed.RatifiedSteps...),
	}, nil
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
