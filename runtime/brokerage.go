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
	"github.com/idolum-ai/aphelion/turn"
)

const (
	maxBrokerageRounds = 3
)

type turnBrokerage struct {
	Active                     bool
	Phase                      string
	IdolumNote                 string
	SuggestedExecutionContract *pipeline.ExecutionContract
	Ratification               string
	SignalJudgment             string
	RatificationRecord         string
	RatifiedSteps              []string
	RatifiedExecutionContract  *pipeline.ExecutionContract
}

func seedTurnBrokerageFromFaceNote(note string) turnBrokerage {
	trimmed := strings.TrimSpace(note)
	if trimmed == "" {
		return turnBrokerage{}
	}
	brokerage := turnBrokerage{
		Active:     true,
		IdolumNote: trimmed,
	}
	if suggestedContract := pipeline.ParseExecutionContract(trimmed); suggestedContract != nil {
		brokerage.Phase = brokeragePhaseName(brokerage.Active, "brokerage")
		brokerage.SuggestedExecutionContract = suggestedContract
	} else {
		brokerage.Phase = brokeragePhaseName(brokerage.Active, "proposal")
	}
	return brokerage
}

func (b turnBrokerage) toTurnAwareness() turn.BrokerageAwareness {
	return turn.BrokerageAwareness{
		Active:                     b.Active,
		Phase:                      b.Phase,
		SuggestedExecutionContract: b.SuggestedExecutionContract,
		Ratification:               strings.TrimSpace(b.Ratification),
		RatifiedExecutionContract:  b.RatifiedExecutionContract,
		SignalJudgment:             strings.TrimSpace(b.SignalJudgment),
	}
}

func parseBrokerageRatification(text string) (turnBrokerage, error) {
	parsed, err := pipeline.ParseBrokerageRatification(text)
	if err != nil {
		return turnBrokerage{}, err
	}
	contract := pipeline.ExecutionContract(parsed.RatifiedContract)
	return turnBrokerage{
		RatificationRecord:        parsed.RawText,
		Ratification:              string(parsed.Disposition),
		SignalJudgment:            string(parsed.SignalJudgment),
		RatifiedExecutionContract: &contract,
		RatifiedSteps:             append([]string(nil), parsed.RatifiedSteps...),
	}, nil
}

func (r *Runtime) ratifyTurnBrokerage(
	ctx context.Context,
	exec pipeline.TurnExecutionContract,
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
			IdolumProposal: brokerage.IdolumNote,
			RatifiedExecutionContract: turn.ApplyBrokerageAwareness(prompt.RuntimeAwareness{}, brokerage.toTurnAwareness()).
				RatifiedExecutionContract,
			Ratification:       brokerage.Ratification,
			SignalJudgment:     brokerage.SignalJudgment,
			RatifiedSteps:      brokerage.RatifiedSteps,
			RatificationRecord: brokerage.RatificationRecord,
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
	exec pipeline.TurnExecutionContract,
	baseAwareness prompt.RuntimeAwareness,
	systemBlocks []agent.SystemBlock,
	history []agent.Message,
	userText string,
	brokerage turnBrokerage,
	requestFaceNote brokerageFaceRequester,
	audit *turnAuditRecorder,
) (turnBrokerage, core.TokenUsage) {
	return turn.ConvergeBrokerage(ctx, turn.BrokerageConvergeInput[turnBrokerage]{
		Initial:   brokerage,
		MaxRounds: maxBrokerageRounds,
		Note: func(state turnBrokerage) string {
			return strings.TrimSpace(state.IdolumNote)
		},
		Phase: func(state turnBrokerage) string {
			return strings.TrimSpace(state.Phase)
		},
		Ratification: func(state turnBrokerage) string {
			return strings.TrimSpace(state.Ratification)
		},
		Ratify: func(ctx context.Context, _ int, state turnBrokerage) (turnBrokerage, core.TokenUsage, error) {
			return r.ratifyTurnBrokerage(ctx, exec, systemBlocks, history, userText, state)
		},
		Revise: func(_ context.Context, _ int, state turnBrokerage) (turnBrokerage, core.TokenUsage, error) {
			reviseAwareness := turn.ApplyBrokerageAwareness(baseAwareness, state.toTurnAwareness())
			reviseAwareness.ArtifactMode = "scene"
			revised, proposalUsage, proposalErr := requestFaceNote("brokerage", reviseAwareness, state.IdolumNote, state.RatificationRecord)
			if proposalErr != nil {
				return state, proposalUsage, proposalErr
			}
			if strings.TrimSpace(revised) == "" {
				return state, proposalUsage, fmt.Errorf("empty brokerage revision")
			}
			state.IdolumNote = strings.TrimSpace(revised)
			state.SuggestedExecutionContract = pipeline.ParseExecutionContract(revised)
			state.Ratification = ""
			state.SignalJudgment = ""
			state.RatificationRecord = ""
			state.RatifiedSteps = nil
			state.RatifiedExecutionContract = nil
			return state, proposalUsage, nil
		},
		Fallback: func(ctx context.Context, state turnBrokerage) (turnBrokerage, core.TokenUsage) {
			fallback, fallbackUsage := r.fallbackToPlainProposal(ctx, baseAwareness, state, requestFaceNote, core.TokenUsage{})
			return fallback, fallbackUsage
		},
		OnRound: func(round int, before turnBrokerage, after turnBrokerage, err error) {
			if audit == nil {
				return
			}
			roundAudit := BrokerageRoundAudit{
				Round:      round,
				Phase:      before.Phase,
				IdolumNote: strings.TrimSpace(before.IdolumNote),
				SuggestedExecutionContract: turn.ApplyBrokerageAwareness(prompt.RuntimeAwareness{}, before.toTurnAwareness()).
					SuggestedExecutionContract,
			}
			if err != nil {
				roundAudit.Error = err.Error()
				audit.RecordBrokerageRound(roundAudit)
				return
			}
			roundAudit.Ratification = strings.TrimSpace(after.Ratification)
			roundAudit.RatifiedExecutionContract = turn.ApplyBrokerageAwareness(prompt.RuntimeAwareness{}, after.toTurnAwareness()).
				RatifiedExecutionContract
			roundAudit.SignalJudgment = strings.TrimSpace(after.SignalJudgment)
			roundAudit.RatifiedSteps = append([]string(nil), after.RatifiedSteps...)
			audit.RecordBrokerageRound(roundAudit)
		},
		OnConverged: func(converged bool) {
			if audit != nil {
				audit.MarkBrokerageConverged(converged)
			}
		},
	})
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
