//go:build linux

package runtime

import (
	"strings"
	"time"
	"unicode"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const continuationCandidateWorkingObjectiveFreshness = 6 * time.Hour

type continuationCandidateViability struct {
	Live               bool
	Reason             string
	WorkingObjective   string
	CandidateObjective string
}

func (r *Runtime) operationContinuationCandidateViability(key session.SessionKey, opState session.OperationState, now time.Time) continuationCandidateViability {
	viability := continuationCandidateViability{Live: true}
	if r == nil || r.store == nil {
		return viability
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	working, err := r.store.WorkingObjective(key)
	if err != nil {
		return viability
	}
	working = session.NormalizeWorkingObjective(working)
	if !workingObjectiveCanSuppressContinuationCandidate(working, now) {
		return viability
	}
	opState = session.NormalizeOperationState(opState)
	candidate := operationContinuationCandidateText(opState)
	if continuationCandidateTextMatchesWorkingObjective(candidate, working.Objective) {
		return viability
	}
	return continuationCandidateViability{
		Live:               false,
		Reason:             recoveryCandidateReasonStaleVsWorkingObjective,
		WorkingObjective:   working.Objective,
		CandidateObjective: firstNonEmptyContinuation(opState.Objective, opState.Summary, opState.PhasePlan.Goal, opState.Proposal.Summary),
	}
}

// Working objectives are allowed to suppress stale durable candidates only while
// they are fresh, high-confidence current intent. Older objectives become
// background evidence again so the operator is not trapped by stale suppression.
func workingObjectiveCanSuppressContinuationCandidate(working session.WorkingObjective, now time.Time) bool {
	if strings.TrimSpace(working.Objective) == "" {
		return false
	}
	if !working.ExpiresAt.IsZero() && !working.ExpiresAt.After(now) {
		return false
	}
	if !working.CreatedAt.IsZero() && now.Sub(working.CreatedAt.UTC()) > continuationCandidateWorkingObjectiveFreshness {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(working.Confidence), "high")
}

func operationContinuationCandidateText(opState session.OperationState) string {
	opState = session.NormalizeOperationState(opState)
	parts := []string{
		opState.ID,
		opState.Objective,
		opState.Stage,
		opState.Summary,
		opState.Proposal.Kind,
		opState.Proposal.OperatorTitle,
		opState.Proposal.PlanTitle,
		opState.Proposal.Summary,
		opState.Proposal.WhyNow,
		opState.Proposal.BoundedEffect,
		opState.PhasePlan.ID,
		opState.PhasePlan.Goal,
		opState.PhasePlan.CurrentPhaseID,
	}
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if !phase.Active() {
			continue
		}
		parts = append(parts,
			phase.ID,
			phase.OperatorTitle,
			phase.PlanTitle,
			phase.Summary,
			phase.AuthorityClass,
			phase.WhyNow,
			phase.BoundedEffect,
		)
		parts = append(parts, phase.AllowedActions...)
	}
	return strings.Join(parts, " ")
}

func continuationCandidateTextMatchesWorkingObjective(candidate string, objective string) bool {
	objectiveTokens := continuationCandidateMeaningfulTokens(objective)
	if len(objectiveTokens) == 0 {
		return true
	}
	candidateTokens := continuationCandidateMeaningfulTokens(candidate)
	for token := range objectiveTokens {
		if _, ok := candidateTokens[token]; ok {
			return true
		}
	}
	return false
}

func continuationCandidateMeaningfulTokens(text string) map[string]struct{} {
	tokens := map[string]struct{}{}
	var b strings.Builder
	flush := func() {
		token := strings.ToLower(strings.TrimSpace(b.String()))
		b.Reset()
		if len(token) < 4 || continuationCandidateStopword(token) {
			return
		}
		tokens[token] = struct{}{}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func continuationCandidateStopword(token string) bool {
	switch token {
	case "about", "action", "approval", "approve", "bounded", "chat", "check", "continue", "continuation", "current", "draft", "evidence", "finding", "findings", "fresh", "generate", "inspect", "local", "metadata", "next", "objective", "operation", "phase", "plan", "proposal", "report", "request", "resume", "review", "state", "step", "work":
		return true
	default:
		return false
	}
}

func (r *Runtime) recordSuppressedOperationContinuationCandidate(key session.SessionKey, opState session.OperationState, viability continuationCandidateViability, now time.Time) {
	if r == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationCandidateSuppressed, "continuation", "suppressed", map[string]any{
		"reason":              strings.TrimSpace(viability.Reason),
		"operation_id":        strings.TrimSpace(opState.ID),
		"operation_objective": strings.TrimSpace(opState.Objective),
		"working_objective":   strings.TrimSpace(viability.WorkingObjective),
		"candidate_objective": strings.TrimSpace(viability.CandidateObjective),
	}, now.UTC())
}
