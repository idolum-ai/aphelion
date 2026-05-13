//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/idolum-ai/aphelion/session"
)

func continuationUserFacingPlanLabel(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	title := continuationUserFacingPlanTitle(state)
	phase := continuationUserFacingPhaseLabel(state)
	if title == "" && phase == "" {
		return ""
	}
	if title == "" {
		title = phase
		phase = ""
	}
	if phase != "" && !continuationTitleContainsPhase(title, phase) {
		title += " (" + phase + ")"
	}
	return "Plan: " + title
}

func continuationUserFacingPlanTitle(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if title := firstNonEmptyContinuation(
		state.ActionProposal.OperatorTitle,
		state.ActionProposal.PlanTitle,
		state.ContinuationLease.OperatorTitle,
		state.ContinuationLease.PlanTitle,
	); title != "" {
		return continuationPlanTitleFromText(title)
	}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if title := firstNonEmptyContinuation(phase.OperatorTitle, phase.PlanTitle); title != "" {
			return continuationPlanTitleFromText(title)
		}
	}
	texts := []string{
		state.StageSummary,
		state.ActionProposal.Summary,
		state.Objective,
		state.ActionProposal.OperationID,
		state.DecisionID,
		state.ContinuationLease.ProposalID,
		state.ContinuationLease.ID,
	}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		texts = append(texts, phase.Summary, phase.OperationPhaseID, phase.ID)
	}
	if title := continuationNamedAgentPlanTitle(strings.Join(texts, "\n")); title != "" {
		return title
	}
	for _, candidate := range []string{state.ActionProposal.Summary, state.Objective, state.StageSummary} {
		if title := cleanContinuationPlanTitleCandidate(candidate); title != "" {
			return title
		}
	}
	if subject := continuationApprovalButtonSubject(state); subject != "" {
		return subject
	}
	return ""
}

func continuationPlanTitleFromText(text string) string {
	return cleanContinuationPlanTitleCandidate(text)
}

func continuationNamedAgentPlanTitle(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" || !strings.Contains(lower, "agent") {
		return ""
	}
	subject := ""
	switch {
	case strings.Contains(lower, "job") || strings.Contains(lower, "career"):
		subject = "Job Agent"
	case strings.Contains(lower, "telegram"):
		subject = "Telegram Agent"
	default:
		return ""
	}
	if name := continuationHumanNameCandidate(text); name != "" {
		return name + "'s " + subject
	}
	return subject
}

func continuationHumanNameCandidate(text string) string {
	replacer := strings.NewReplacer(
		"-", " ",
		"_", " ",
		":", " ",
		"/", " ",
		"\\", " ",
		".", " ",
		",", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
	)
	for _, field := range strings.Fields(replacer.Replace(strings.TrimSpace(text))) {
		name := strings.Trim(field, "'\"`")
		name = strings.TrimSuffix(strings.TrimSuffix(name, "'s"), "’s")
		if continuationLooksLikeHumanName(name) {
			return name
		}
	}
	return ""
}

func continuationLooksLikeHumanName(token string) bool {
	token = strings.TrimSpace(token)
	runes := []rune(token)
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	if !unicode.IsUpper(runes[0]) {
		return false
	}
	allUpper := true
	for _, r := range runes[1:] {
		if unicode.IsLower(r) {
			allUpper = false
			break
		}
	}
	if allUpper {
		return false
	}
	return !continuationHumanNameStopWord(strings.ToLower(token))
}

func continuationHumanNameStopWord(word string) bool {
	switch strings.TrimSpace(word) {
	case "", "approve", "approval", "bounded", "bundle", "child", "consent", "create", "current", "execute", "fresh", "intake", "job", "later", "phase", "plan", "profile", "public", "resume", "run", "stage", "stages", "superseded", "telegram", "the", "this", "use":
		return true
	default:
		return false
	}
}

func cleanContinuationPlanTitleCandidate(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || continuationLooksLikeSystemIdentifier(value) {
		return ""
	}
	if idx := strings.IndexAny(value, "\n\r"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "approve plan budget:") {
		if idx := strings.LastIndex(lower, " for "); idx >= 0 {
			return cleanContinuationPlanTitleCandidate(value[idx+5:])
		}
		return ""
	}
	for _, prefix := range []string{
		"approve stage",
		"approve stages",
		"approve phase",
		"approval needed",
		"continuation approval",
		"revoked continuation",
	} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	value = strings.TrimSpace(strings.TrimRight(value, "."))
	runes := []rune(value)
	if len(runes) > 72 {
		value = strings.TrimSpace(string(runes[:72])) + "..."
	}
	return value
}

func continuationLooksLikeSystemIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.Contains(lower, "lease-") || strings.Contains(lower, "aprop-") {
		return true
	}
	if len(strings.Fields(value)) == 1 && len(value) > 32 && strings.ContainsAny(value, "-_") {
		return true
	}
	return false
}

func continuationUserFacingPhaseLabel(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	candidates := make([]string, 0, 8)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		candidates = append(candidates, phase.OperationPhaseID, phase.ID, phase.Summary)
	}
	candidates = append(candidates,
		state.ActionProposal.OperationID,
		state.ActionProposal.Summary,
		state.StageSummary,
		state.DecisionID,
		state.ContinuationLease.ProposalID,
		state.ActionProposal.ID,
	)
	for _, candidate := range candidates {
		if token := continuationPhaseTokenFromText(candidate); token != "" {
			return "Phase " + token
		}
	}
	return ""
}

func continuationPhaseTokenFromText(raw string) string {
	fields := continuationSubjectFields(raw)
	for i := 0; i < len(fields); i++ {
		field := strings.ToLower(strings.TrimSpace(fields[i]))
		if field == "phase" && i+1 < len(fields) {
			if token := normalizeContinuationPhaseToken(fields[i+1]); token != "" {
				return token
			}
		}
		if strings.HasPrefix(field, "phase") && len(field) > len("phase") {
			if token := normalizeContinuationPhaseToken(field[len("phase"):]); token != "" {
				return token
			}
		}
	}
	return ""
}

func continuationTitleContainsPhase(title string, phase string) bool {
	title = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(title)), " "))
	phase = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(phase)), " "))
	return phase != "" && strings.Contains(title, phase)
}

func compactContinuationPhaseSubject(raw string) string {
	fields := continuationSubjectFields(raw)
	if len(fields) == 0 {
		return ""
	}
	for i := 0; i < len(fields); i++ {
		field := strings.ToLower(strings.TrimSpace(fields[i]))
		if field == "" {
			continue
		}
		phaseToken := ""
		restStart := i + 1
		if field == "phase" && i+1 < len(fields) {
			phaseToken = normalizeContinuationPhaseToken(fields[i+1])
			restStart = i + 2
		} else if strings.HasPrefix(field, "phase") && len(field) > len("phase") {
			phaseToken = normalizeContinuationPhaseToken(field[len("phase"):])
		}
		if phaseToken == "" {
			continue
		}
		words := make([]string, 0, 3)
		for j := restStart; j < len(fields) && len(words) < 3; j++ {
			word := normalizeContinuationSubjectWord(fields[j])
			if word == "" || continuationSubjectStopWord(strings.ToLower(word)) {
				continue
			}
			words = append(words, word)
		}
		subject := "Phase " + phaseToken
		if len(words) > 0 {
			subject += " " + strings.Join(words, " ")
		}
		return subject
	}
	return ""
}

func continuationSubjectFields(raw string) []string {
	replacer := strings.NewReplacer(
		"-", " ",
		"_", " ",
		":", " ",
		"/", " ",
		"\\", " ",
		".", " ",
		",", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
	)
	return strings.Fields(replacer.Replace(strings.TrimSpace(raw)))
}

func normalizeContinuationPhaseToken(token string) string {
	var b strings.Builder
	hasDigit := false
	for _, r := range strings.TrimSpace(token) {
		if unicode.IsDigit(r) {
			hasDigit = true
			b.WriteRune(r)
			continue
		}
		if unicode.IsLetter(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	if !hasDigit {
		return ""
	}
	return b.String()
}

func normalizeContinuationSubjectWord(word string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(word) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	out := b.String()
	switch out {
	case "ui":
		return "UI"
	case "ux":
		return "UX"
	case "id":
		return "ID"
	default:
		return out
	}
}

func continuationSubjectStopWord(word string) bool {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "", "a", "an", "the", "and", "or", "to", "of", "for", "in", "on", "one", "next", "safe", "bounded", "bundle", "bundled", "rebundled", "read", "readonly", "only", "adapter", "local", "child", "idolum", "status", "check", "lane", "remaining", "run":
		return true
	default:
		return false
	}
}

func approvedContinuationEventTextForState(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	lines := []string{approvedContinuationEventText, "", "Approved work:"}
	if label := continuationUserFacingPlanLabel(state); label != "" {
		lines = append(lines, label)
	}
	if next := continuationApprovedNextStepLine(state); next != "" {
		lines = append(lines, "Next: "+next)
	}
	if scope := continuationApprovedScopeLine(state); scope != "" {
		lines = append(lines, "Scope: "+scope)
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Budget: up to %d %s.", state.RemainingTurns, continuationTurnWord(state.RemainingTurns)))
	}
	if stops := continuationApprovalPromptStops(state); len(stops) > 0 {
		lines = append(lines, "Stops before: "+strings.Join(stops, ", ")+".")
	}
	if continuationActionIsPlanLeaseApproval(state) {
		if state.ApprovalBundle.Active() {
			lines = append(lines, "This approval covers the named plan budget only.")
		} else {
			lines = append(lines, "This records the plan budget approval; execution still stops at hard gates.")
		}
	}
	return strings.Join(lines, "\n")
}

func continuationApprovedNextStepLine(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	candidates := []string{state.StageSummary, state.ActionProposal.Summary}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		candidates = append([]string{phase.Summary}, candidates...)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || continuationLooksLikeSystemIdentifier(candidate) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(candidate), "approve ") {
			if idx := strings.Index(candidate, ":"); idx >= 0 && idx+1 < len(candidate) {
				candidate = strings.TrimSpace(candidate[idx+1:])
			}
		}
		if line := continuationPromptCompactLine(candidate, 180); line != "" {
			return line
		}
	}
	return ""
}

func continuationApprovedScopeLine(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if scope := continuationPromptCompactLine(phase.BoundedEffect, 220); scope != "" {
			return scope
		}
	}
	for _, candidate := range []string{state.ActionProposal.BoundedEffect, state.GovernorIntent.Constraints} {
		if scope := continuationPromptCompactLine(candidate, 240); scope != "" {
			return scope
		}
	}
	return ""
}
