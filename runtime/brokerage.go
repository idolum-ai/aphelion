//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

type turnBrokerage struct {
	Active                     bool
	Phase                      string
	IdolumNote                 string
	SuggestedExecutionContract *pipeline.ExecutionContract
	Ratification               string
	SignalJudgment             string
	RatificationSurface        string
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

func (r *Runtime) recordBrokerageControlFlowJudgment(key session.SessionKey, brokerage turnBrokerage, now time.Time) error {
	if r == nil || r.store == nil || strings.TrimSpace(brokerage.IdolumNote) == "" {
		return nil
	}
	payload := map[string]any{
		"phase":                        brokerage.Phase,
		"ratification":                 brokerage.Ratification,
		"signal_judgment":              brokerage.SignalJudgment,
		"suggested_execution_contract": brokerage.SuggestedExecutionContract,
		"ratified_execution_contract":  brokerage.RatifiedExecutionContract,
		"ratified_steps":               brokerage.RatifiedSteps,
		"proposal_fingerprint":         brokerageProposalFingerprint(brokerage),
		"contract_fingerprint":         brokerageContractFingerprint(brokerage),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode brokerage control-flow judgment: %w", err)
	}
	completeness := session.JudgmentCompletenessComplete
	var unknowns []session.UnknownPredicate
	if strings.TrimSpace(brokerage.Ratification) == "" || brokerage.RatifiedExecutionContract == nil {
		completeness = session.JudgmentCompletenessPartial
		unknowns = append(unknowns, session.UnknownPredicate{Kind: "incomplete_brokerage_contract", Reason: "brokerage did not produce a complete ratified execution contract"})
	}
	judgment, err := r.store.RecordJudgment(session.JudgmentInput{
		Key:                key,
		Kind:               session.JudgmentKindBrokerageControlFlow,
		SchemaVersion:      "v1",
		SubjectKey:         "brokerage:" + brokerageControlFlowHash(brokerage),
		ClaimKey:           "turn_control_flow_contract",
		InterpreterID:      "runtime.brokerage_convergence",
		InterpreterVersion: "v1",
		InputRefs:          []string{session.JudgmentUseHashRef("brokerage_note", brokerage.IdolumNote)},
		InputHash:          brokerageControlFlowHash(brokerage),
		ResultJSON:         string(raw),
		Completeness:       completeness,
		Unknowns:           unknowns,
		DependencyRefs: []session.JudgmentDependencyRef{
			{Kind: "brokerage_proposal", Ref: session.JudgmentUseHashRef("text", brokerage.IdolumNote), Role: "support"},
		},
		SourceFaultDomains: []string{"model_brokerage", "pipeline_brokerage_parser_v1"},
		Sensitivity:        "brokerage_metadata",
		AsOf:               now,
		CreatedAt:          now,
	})
	if err != nil {
		return err
	}
	_, err = r.store.RecordJudgmentUseCommitment(session.JudgmentUseInput{
		Key:                  key,
		ConsumerID:           session.ConsumerRuntimeBrokerageControlFlow,
		Consequence:          session.JudgmentUseConsequenceControlFlow,
		JudgmentRefs:         []string{session.JudgmentRef(judgment.ID)},
		DependencyRefs:       judgment.DependencyRefs,
		PolicyRef:            "brokerage_control_flow_v1",
		ResultRef:            session.JudgmentUseHashRef("brokerage_control_flow", brokerageControlFlowHash(brokerage)),
		Irreversible:         false,
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		Reason:               "brokerage control-flow contract selected",
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	return err
}

func brokerageControlFlowHash(brokerage turnBrokerage) string {
	seed := strings.Join([]string{
		brokerage.IdolumNote,
		brokerage.Ratification,
		brokerage.SignalJudgment,
		brokerageProposalFingerprint(brokerage),
		brokerageContractFingerprint(brokerage),
		strings.Join(brokerage.RatifiedSteps, "\x00"),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
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
	if advisory := prompt.RenderIdolumBrokerageForGovernor(r.faceName(), brokerage.IdolumNote); advisory != "" {
		messages = append(messages, agent.Message{Role: "system", Content: advisory})
	}
	messages = append(messages, history...)
	messages = append(messages, agent.Message{
		Role: "user",
		Content: strings.Join([]string{
			"The latest user message is below.",
			"Before the main turn executes, ratify how this turn should proceed.",
			"Return exactly this structure and nothing else:",
			"CONTINUATION_SCHEMA_VERSION: 1",
			"INSPECT: <yes|no>",
			"QUESTION: <yes|no>",
			"ANSWER: <yes|no>",
			"RATIFICATION: <accept|adapt|reject>",
			"SIGNAL_JUDGMENT: <confirmed|overridden|not_material>  # optional; use when Idolum named a hidden input",
			"PLAN:",
			"- <short concrete first step>",
			"- <optional second step>",
			"- <optional third step>",
			"CONTINUATION_INTENT: <continue|hold|stop>",
			"CONTINUATION_RATIONALE: <short rationale>",
			"CONTINUATION_RATIFIED: <yes|no>",
			"CONTINUATION_NEXT_STEP: <short next bounded step>",
			"CONTINUATION_CONSTRAINTS: <short bounded constraints>",
			"CONTINUATION_CONFIDENCE: <low|medium|high>",
			"",
			"If you want one short live progress update during this internal ratification exchange, append this optional markdown block:",
			"### Surface",
			"<one short user-facing progress line>",
			"Only text inside that Surface block is surfaced live during this exchange; all other text here stays internal.",
			"",
			"Convergence criteria:",
			"- Use RATIFICATION: accept only when the execution contract is safe, authorized, and specific enough to execute.",
			"- Use RATIFICATION: adapt only when a concrete revision can plausibly resolve the remaining disagreement.",
			"- Use RATIFICATION: reject when the next step crosses authority, capability, privacy, external-effect, or irreversible-risk boundaries, or when further internal deliberation is only repeating the same unresolved objection.",
			"- Do not seek full unanimity about style or preference before execution; preserve material disagreements in the plan when the execution contract is still defensible.",
			"",
			"User message:",
			strings.TrimSpace(userText),
		}, "\n"),
	})

	resp, err := completeProvider(ctx, exec.Provider, messages, nil, r.reasoningOptionsForRun(session.TurnRunKindInteractive))
	if err != nil {
		return brokerage, core.TokenUsage{}, err
	}

	surfaceText, parseText := extractDeliberationSurfaceMarkdown(resp.Content)
	parsed, parseErr := parseBrokerageRatification(parseText)
	if parseErr != nil {
		return brokerage, resp.Usage, fmt.Errorf("parse brokerage ratification: %w", parseErr)
	}
	brokerage.RatificationSurface = strings.TrimSpace(surfaceText)
	brokerage.RatificationRecord = parsed.RatificationRecord
	brokerage.Ratification = parsed.Ratification
	brokerage.SignalJudgment = parsed.SignalJudgment
	brokerage.RatifiedExecutionContract = parsed.RatifiedExecutionContract
	brokerage.RatifiedSteps = append(brokerage.RatifiedSteps[:0], parsed.RatifiedSteps...)
	return brokerage, resp.Usage, nil
}

func brokerageContextForGovernor(faceName string, brokerage turnBrokerage) string {
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
		return prompt.RenderIdolumBrokerageForGovernor(faceName, brokerage.IdolumNote)
	}
	return prompt.RenderIdolumProposalForGovernor(faceName, brokerage.IdolumNote)
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

type brokerageFaceRequester func(ctx context.Context, mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error)

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
	emitSurface func(ctx context.Context, text string),
) (turnBrokerage, core.TokenUsage) {
	return turn.ConvergeBrokerage(ctx, turn.BrokerageConvergeInput[turnBrokerage]{
		Initial: brokerage,
		Policy:  r.brokerageConvergencePolicy(),
		Note: func(state turnBrokerage) string {
			return strings.TrimSpace(state.IdolumNote)
		},
		Phase: func(state turnBrokerage) string {
			return strings.TrimSpace(state.Phase)
		},
		Ratification: func(state turnBrokerage) string {
			return strings.TrimSpace(state.Ratification)
		},
		ContractFingerprint: func(state turnBrokerage) string {
			return brokerageContractFingerprint(state)
		},
		ProposalFingerprint: func(state turnBrokerage) string {
			return brokerageProposalFingerprint(state)
		},
		Ratify: func(ctx context.Context, _ int, state turnBrokerage) (turnBrokerage, core.TokenUsage, error) {
			return r.ratifyTurnBrokerage(ctx, exec, systemBlocks, history, userText, state)
		},
		Revise: func(ctx context.Context, _ int, state turnBrokerage) (turnBrokerage, core.TokenUsage, error) {
			reviseAwareness := turn.ApplyBrokerageAwareness(baseAwareness, state.toTurnAwareness())
			reviseAwareness.ArtifactMode = "scene"
			revised, proposalUsage, proposalErr := requestFaceNote(ctx, "brokerage", reviseAwareness, state.IdolumNote, state.RatificationRecord)
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
			state.RatificationSurface = ""
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
			if emitSurface != nil {
				emitSurface(ctx, after.RatificationSurface)
			}
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
		OnStop: func(stop turn.BrokerageStop) {
			if audit != nil {
				audit.MarkBrokerageStopped(stop.Reason, stop.Round, stop.Converged)
			}
		},
	})
}

func (r *Runtime) brokerageConvergencePolicy() turn.BrokerageConvergencePolicy {
	policy := turn.DefaultBrokerageConvergencePolicy()
	if r == nil || r.cfg == nil {
		return policy
	}
	cfg := r.cfg.Governor.Brokerage
	if cfg.MinRounds == 0 &&
		cfg.MaxRounds == 0 &&
		cfg.AbsoluteMaxRounds == 0 &&
		strings.TrimSpace(cfg.MaxElapsed) == "" &&
		cfg.StableContractRounds == 0 &&
		!cfg.StopOnStableContract &&
		!cfg.StopOnRepeatedProposal &&
		!cfg.StopOnReject {
		return policy
	}
	policy.MinRounds = cfg.MinRounds
	policy.MaxRounds = cfg.MaxRounds
	policy.AbsoluteMaxRounds = cfg.AbsoluteMaxRounds
	if elapsed, err := time.ParseDuration(strings.TrimSpace(cfg.MaxElapsed)); err == nil {
		policy.MaxElapsed = elapsed
	}
	policy.StableContractRounds = cfg.StableContractRounds
	policy.StopOnStableContract = cfg.StopOnStableContract
	policy.StopOnRepeatedProposal = cfg.StopOnRepeatedProposal
	policy.StopOnReject = cfg.StopOnReject
	return turn.NormalizeBrokerageConvergencePolicy(policy)
}

func brokerageContractFingerprint(state turnBrokerage) string {
	parts := []string{
		strings.TrimSpace(state.Ratification),
		strings.TrimSpace(state.SignalJudgment),
	}
	if state.RatifiedExecutionContract != nil {
		parts = append(parts, state.RatifiedExecutionContract.Summary())
	}
	for _, step := range state.RatifiedSteps {
		if trimmed := strings.TrimSpace(step); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return normalizeBrokerageFingerprint(strings.Join(parts, "\n"))
}

func brokerageProposalFingerprint(state turnBrokerage) string {
	return normalizeBrokerageFingerprint(state.IdolumNote)
}

func normalizeBrokerageFingerprint(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
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
	brokerage.RatificationSurface = ""
	brokerage.RatificationRecord = ""
	brokerage.RatifiedSteps = nil
	brokerage.RatifiedExecutionContract = nil
	brokerage.SuggestedExecutionContract = nil

	proposal, proposalUsage, proposalErr := requestFaceNote(ctx, "proposal", baseAwareness, "", "")
	currentUsage = addTokenUsage(currentUsage, proposalUsage)
	if proposalErr == nil {
		brokerage.IdolumNote = strings.TrimSpace(proposal)
	}
	brokerage.Active = strings.TrimSpace(brokerage.IdolumNote) != ""
	brokerage.Phase = brokeragePhaseName(brokerage.Active, "proposal")
	return brokerage, currentUsage
}
