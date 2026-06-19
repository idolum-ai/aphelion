//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const recoveryCandidateReasonStaleVsWorkingObjective = "stale_vs_working_objective"

type recoveryCandidateArbitration struct {
	Live               bool
	Reason             string
	WorkingObjective   string
	CandidateObjective string
	RequestText        string
}

func (r *Runtime) operationRecoveryCandidateArbitration(key session.SessionKey, msg core.InboundMessage, opState session.OperationState, now time.Time) recoveryCandidateArbitration {
	decision := recoveryCandidateArbitration{Live: true, RequestText: strings.TrimSpace(msg.Text)}
	if r == nil || r.store == nil {
		return decision
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	working, err := r.store.WorkingObjective(key)
	if err != nil {
		return decision
	}
	working = session.NormalizeWorkingObjective(working)
	if !workingObjectiveCanSuppressContinuationCandidate(working, now) {
		return decision
	}
	opState = session.NormalizeOperationState(opState)
	candidate := operationContinuationCandidateText(opState)
	if continuationCandidateTextMatchesWorkingObjective(candidate, working.Objective) {
		return decision
	}
	if recoveryRequestExplicitlySelectsCandidate(msg.Text, candidate) {
		return decision
	}
	return recoveryCandidateArbitration{
		Live:               false,
		Reason:             recoveryCandidateReasonStaleVsWorkingObjective,
		WorkingObjective:   working.Objective,
		CandidateObjective: firstNonEmptyContinuation(opState.Objective, opState.Summary, opState.PhasePlan.Goal, opState.Proposal.Summary),
		RequestText:        strings.TrimSpace(msg.Text),
	}
}

func recoveryRequestExplicitlySelectsCandidate(request string, candidate string) bool {
	request = strings.TrimSpace(request)
	if request == "" || strings.TrimSpace(candidate) == "" {
		return false
	}
	lower := strings.ToLower(request)
	if recoveryRequestNegatesResumeIntent(lower) || !recoveryRequestHasResumeIntent(lower) {
		return false
	}
	return continuationCandidateTextMatchesWorkingObjective(candidate, request)
}

func recoveryRequestHasResumeIntent(lower string) bool {
	switch {
	case strings.Contains(lower, "resume"),
		strings.Contains(lower, "continue"),
		strings.Contains(lower, "pick up"),
		strings.Contains(lower, "go back"),
		strings.Contains(lower, "return to"),
		strings.Contains(lower, "revisit"),
		strings.Contains(lower, "switch back"):
		return true
	default:
		return false
	}
}

func recoveryRequestNegatesResumeIntent(lower string) bool {
	negated := []string{
		"don't resume", "do not resume", "dont resume", "not resume", "without resuming",
		"don't continue", "do not continue", "dont continue", "not continue", "without continuing",
		"don't revisit", "do not revisit", "dont revisit", "not revisit", "without revisiting",
		"don't pull", "do not pull", "dont pull", "not pull", "without pulling",
		"don't use", "do not use", "dont use", "not use", "without using",
	}
	for _, phrase := range negated {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func (r *Runtime) recordSuppressedRecoveryCandidate(key session.SessionKey, opState session.OperationState, decision recoveryCandidateArbitration, surface string, now time.Time) {
	if r == nil || r.store == nil || decision.Live {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	opState = session.NormalizeOperationState(opState)
	r.recordExecutionEvent(key, core.ExecutionEventRecoveryCandidateSuppressed, "recovery", "suppressed", recoveryCandidateSuppressedPayload(opState, decision, surface), now.UTC())
}

func recoveryCandidateSuppressedPayload(opState session.OperationState, decision recoveryCandidateArbitration, surface string) map[string]any {
	return map[string]any{
		"reason":              strings.TrimSpace(decision.Reason),
		"surface":             strings.TrimSpace(surface),
		"operation_id":        strings.TrimSpace(opState.ID),
		"operation_objective": strings.TrimSpace(opState.Objective),
		"operation_status":    strings.TrimSpace(string(opState.Status)),
		"working_objective":   strings.TrimSpace(decision.WorkingObjective),
		"candidate_objective": strings.TrimSpace(decision.CandidateObjective),
		"request_text":        strings.TrimSpace(decision.RequestText),
	}
}
