//go:build linux

package turn

import (
	"strings"

	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

// HiddenInputAwareness is the assembled hidden-input view that informs turn
// orchestration.
type HiddenInputAwareness struct {
	Active            bool
	Categories        []string
	ProvenanceSummary string
	InteriorSignals   []string
}

// EventAwareness captures the explicit origin of the inbound event.
type EventAwareness struct {
	Origin                string
	TurnAuthorizationKind string
}

// ApplyEventAwareness composes inbound-event provenance into runtime awareness.
func ApplyEventAwareness(aw prompt.RuntimeAwareness, event EventAwareness) prompt.RuntimeAwareness {
	aw.EventOrigin = strings.TrimSpace(event.Origin)
	aw.TurnAuthorizationKind = strings.TrimSpace(event.TurnAuthorizationKind)
	return aw
}

// BrokerageAwareness is the composed brokerage signal for a single turn.
type BrokerageAwareness struct {
	Active                     bool
	Phase                      string
	SuggestedExecutionContract *pipeline.ExecutionContract
	Ratification               string
	RatifiedExecutionContract  *pipeline.ExecutionContract
	SignalJudgment             string
}

// ApplyHiddenInputAwareness composes hidden-input metadata into runtime awareness.
func ApplyHiddenInputAwareness(aw prompt.RuntimeAwareness, input HiddenInputAwareness) prompt.RuntimeAwareness {
	aw.HiddenInputsActive = input.Active
	aw.HiddenInputCategories = append([]string(nil), input.Categories...)
	aw.ProvenanceSummary = strings.TrimSpace(input.ProvenanceSummary)
	aw.InteriorSignals = append([]string(nil), input.InteriorSignals...)
	return aw
}

// ApplyPlanAwareness composes plan-state fields from the operational
// current-state store into runtime awareness.
func ApplyPlanAwareness(aw prompt.RuntimeAwareness, state session.PlanState) prompt.RuntimeAwareness {
	return ApplyPlanAwarenessWithEvents(aw, state, nil)
}

func ApplyPlanAwarenessWithEvents(aw prompt.RuntimeAwareness, state session.PlanState, events []session.PlanEvent) prompt.RuntimeAwareness {
	state = session.NormalizePlanState(state)
	aw.PlanActive = len(state.Steps) > 0
	aw.PlanSummary = strings.TrimSpace(state.Explanation)
	aw.PlanSteps = append([]string(nil), state.FormattedSteps()...)
	aw.PlanEvents = session.SemanticPlanEventProjections(events, 5)
	return aw
}

// ApplyOperationAwareness composes operation-state fields from the
// operational current-state store into runtime awareness.
func ApplyOperationAwareness(aw prompt.RuntimeAwareness, state session.OperationState) prompt.RuntimeAwareness {
	state = session.NormalizeOperationState(state)
	aw.OperationActive = state.Active()
	aw.OperationObjective = strings.TrimSpace(state.Objective)
	aw.OperationStatus = strings.TrimSpace(string(state.Status))
	aw.OperationStage = strings.TrimSpace(state.Stage)
	aw.OperationSummary = strings.TrimSpace(state.Summary)
	aw.ProposalActive = state.Proposal.Active()
	aw.ProposalKind = strings.TrimSpace(state.Proposal.Kind)
	aw.ProposalStatus = strings.TrimSpace(string(state.Proposal.Status))
	aw.ProposalSummary = strings.TrimSpace(state.Proposal.Summary)
	aw.ProposalWhyNow = strings.TrimSpace(state.Proposal.WhyNow)
	aw.ProposalBoundedEffect = strings.TrimSpace(state.Proposal.BoundedEffect)
	aw.PhasePlanActive = state.PhasePlan.Active()
	aw.PhasePlanID = strings.TrimSpace(state.PhasePlan.ID)
	aw.PhasePlanGoal = strings.TrimSpace(state.PhasePlan.Goal)
	aw.PhasePlanCurrentPhaseID = strings.TrimSpace(state.PhasePlan.CurrentPhaseID)
	aw.OperationDigest = operationDigestLines(state)
	aw.OperationPhases = aw.OperationPhases[:0]
	for _, phase := range state.PhasePlan.Phases {
		if strings.TrimSpace(phase.ID) == "" && strings.TrimSpace(phase.Summary) == "" {
			continue
		}
		aw.OperationPhases = append(aw.OperationPhases, compactOperationPhaseDigest(phase))
	}
	aw.OperationFindings = aw.OperationFindings[:0]
	for _, finding := range state.Findings {
		if strings.TrimSpace(finding.Claim) == "" {
			continue
		}
		line := "[" + string(finding.Confidence) + "] " + finding.Claim
		if finding.Basis != "" {
			line += " (basis: " + finding.Basis + ")"
		}
		aw.OperationFindings = append(aw.OperationFindings, line)
	}
	aw.OperationArtifacts = aw.OperationArtifacts[:0]
	for _, artifact := range state.Artifacts {
		if strings.TrimSpace(artifact.Ref) == "" {
			continue
		}
		if strings.TrimSpace(artifact.Label) != "" {
			aw.OperationArtifacts = append(aw.OperationArtifacts, artifact.Label+": "+artifact.Ref)
			continue
		}
		aw.OperationArtifacts = append(aw.OperationArtifacts, artifact.Ref)
	}
	return aw
}

func operationDigestLines(state session.OperationState) []string {
	state = session.NormalizeOperationState(state)
	if !state.Active() && strings.TrimSpace(state.ID) == "" {
		return nil
	}
	id := strings.TrimSpace(state.ID)
	if id == "" {
		id = "operation"
	}
	parts := []string{"op " + id}
	if status := strings.TrimSpace(string(state.Status)); status != "" {
		parts = append(parts, status)
	}
	if stage := strings.TrimSpace(state.Stage); stage != "" {
		parts = append(parts, "stage="+stage)
	}
	if !state.UpdatedAt.IsZero() {
		parts = append(parts, "snapshot=operation_state@"+state.UpdatedAt.UTC().Format("20060102T150405Z"))
	}
	if state.PhasePlan.Active() {
		current := strings.TrimSpace(state.PhasePlan.CurrentPhaseID)
		if current == "" && len(state.PhasePlan.Phases) > 0 {
			current = strings.TrimSpace(state.PhasePlan.Phases[0].ID)
		}
		if current != "" {
			parts = append(parts, "current_phase="+current)
		}
	}
	return []string{strings.Join(parts, " · ")}
}

func compactOperationPhaseDigest(phase session.OperationPhase) string {
	label := strings.TrimSpace(phase.ID)
	if label == "" {
		label = compactText(phase.Summary, 72)
	}
	parts := []string{"[" + string(phase.Status) + "] " + label}
	if authority := strings.TrimSpace(phase.AuthorityClass); authority != "" {
		parts = append(parts, "authority="+authority)
	}
	if code := strings.TrimSpace(phase.BlockedReasonCode); code != "" {
		parts = append(parts, "blocked="+code)
	}
	if phase.RequiresConsent {
		parts = append(parts, "requires_consent")
	}
	if phase.RequiresOptIn {
		parts = append(parts, "requires_opt_in")
	}
	if phase.StaleAuthority {
		parts = append(parts, "stale_authority")
	}
	if summary := compactText(phase.Summary, 96); summary != "" && summary != label {
		parts = append(parts, "summary="+summary)
	}
	return strings.Join(parts, " · ")
}

func compactText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-1]) + "…"
}

// ApplyContinuationAwareness composes continuation-handshake signals from the
// operational current-state store into runtime awareness.
func ApplyContinuationAwareness(aw prompt.RuntimeAwareness, state session.ContinuationState) prompt.RuntimeAwareness {
	state = session.NormalizeContinuationState(state)
	aw.ContinuationStatus = strings.TrimSpace(string(state.Status))
	aw.ContinuationActive = state.Active()
	aw.ContinuationPersonaIntent = strings.TrimSpace(string(state.PersonaIntent.Decision))
	aw.ContinuationPersonaWhy = strings.TrimSpace(state.PersonaIntent.Rationale)
	aw.ContinuationGovernorIntent = strings.TrimSpace(string(state.GovernorIntent.Decision))
	aw.ContinuationGovernorWhy = strings.TrimSpace(state.GovernorIntent.Rationale)
	aw.ContinuationRatified = state.GovernorIntent.Ratified
	aw.ContinuationBlockedReason = strings.TrimSpace(state.HandshakeBlockedReason)
	return aw
}

// ApplyBrokerageAwareness composes brokerage context into runtime awareness.
func ApplyBrokerageAwareness(aw prompt.RuntimeAwareness, b BrokerageAwareness) prompt.RuntimeAwareness {
	aw.BrokerageActive = b.Active
	aw.BrokeragePhase = strings.TrimSpace(b.Phase)
	aw.SuggestedExecutionContract = summarizeExecutionContract(b.SuggestedExecutionContract)
	aw.BrokerageRatification = strings.TrimSpace(b.Ratification)
	aw.RatifiedExecutionContract = summarizeExecutionContract(b.RatifiedExecutionContract)
	aw.SignalJudgment = strings.TrimSpace(b.SignalJudgment)
	return aw
}

func summarizeExecutionContract(contract *pipeline.ExecutionContract) string {
	if contract == nil {
		return ""
	}
	return contract.Summary()
}
