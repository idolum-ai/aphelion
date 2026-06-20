//go:build linux

package runtime

import (
	"strings"
	"time"
	"unicode"

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
		working = requestWorkingObjectiveForRecoveryArbitration(msg.Text, recoveryRequestTimestamp(msg), now)
		if !workingObjectiveCanSuppressContinuationCandidate(working, now) {
			return decision
		}
	}
	opState = session.NormalizeOperationState(opState)
	candidate := operationContinuationCandidateText(opState)
	requestNegatesResume := recoveryRequestNegatesResumeIntent(strings.ToLower(msg.Text))
	if !requestNegatesResume && continuationCandidateTextMatchesWorkingObjective(candidate, working.Objective) {
		return decision
	}
	if !requestNegatesResume && recoveryRequestExplicitlySelectsCandidate(msg.Text, candidate) {
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

func recoveryRequestTimestamp(msg core.InboundMessage) time.Time {
	if !msg.Timestamp.IsZero() {
		return msg.Timestamp.UTC()
	}
	if !msg.IngressQueuedAt.IsZero() {
		return msg.IngressQueuedAt.UTC()
	}
	return time.Time{}
}

func requestWorkingObjectiveForRecoveryArbitration(request string, requestAt time.Time, now time.Time) session.WorkingObjective {
	request = strings.TrimSpace(request)
	if request == "" {
		return session.WorkingObjective{}
	}
	if len(continuationCandidateMeaningfulTokens(request)) == 0 {
		return session.WorkingObjective{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if requestAt.IsZero() {
		requestAt = now
	}
	requestAt = requestAt.UTC()
	if now.Sub(requestAt) > continuationCandidateWorkingObjectiveFreshness {
		return session.WorkingObjective{}
	}
	return session.WorkingObjective{
		Objective:  request,
		Source:     "operator_message",
		Confidence: "high",
		CreatedAt:  requestAt,
		ExpiresAt:  requestAt.Add(continuationCandidateWorkingObjectiveFreshness),
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
	return recoverySelectionTextMatchesCandidate(request, candidate)
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

func recoverySelectionTextMatchesCandidate(request string, candidate string) bool {
	requestTokens := recoverySelectionTokens(request)
	if len(requestTokens) == 0 {
		return false
	}
	candidateTokens := recoverySelectionTokens(candidate)
	if len(candidateTokens) == 0 {
		return false
	}
	for token := range requestTokens {
		if _, ok := candidateTokens[token]; ok {
			return true
		}
	}
	return false
}

func recoverySelectionTokens(text string) map[string]struct{} {
	words := recoverySelectionWords(text)
	tokens := map[string]struct{}{}
	add := func(token string) {
		token = strings.TrimSpace(strings.ToLower(token))
		if token != "" {
			tokens[token] = struct{}{}
		}
	}
	for i, word := range words {
		if recoverySelectionWordSignificant(word) {
			add(word)
		}
		if i > 0 && recoverySelectionWordsFormIdentifier(words[i-1], word) {
			add(words[i-1] + ":" + word)
		}
	}
	return tokens
}

func recoverySelectionWords(text string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		word := strings.ToLower(strings.TrimSpace(b.String()))
		b.Reset()
		if word != "" {
			words = append(words, word)
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return words
}

func recoverySelectionWordSignificant(word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" || recoverySelectionStopword(word) {
		return false
	}
	if len(word) >= 4 || recoverySelectionIdentifierWord(word) {
		return true
	}
	return false
}

func recoverySelectionWordsFormIdentifier(left string, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	if (left == "pr" || left == "issue" || left == "gh") && recoverySelectionHasDigit(right) {
		return true
	}
	return false
}

func recoverySelectionIdentifierWord(word string) bool {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "pr", "gh":
		return false
	default:
		return recoverySelectionHasDigit(word)
	}
}

func recoverySelectionHasDigit(word string) bool {
	for _, r := range word {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func recoverySelectionStopword(word string) bool {
	if continuationCandidateStopword(word) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "now", "old", "back", "that", "this", "there", "here", "please":
		return true
	default:
		return false
	}
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
