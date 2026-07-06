//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const maxRuntimeEvidenceContextLines = 8

func (r *Runtime) applyEvidenceHydrationAwareness(ctx context.Context, aw prompt.RuntimeAwareness, key session.SessionKey, runKind session.TurnRunKind, requestText string, sess *session.Session, now time.Time) prompt.RuntimeAwareness {
	if r == nil || r.store == nil || sess == nil {
		return aw
	}
	operationID := strings.TrimSpace(sess.OperationState.ID)
	aw.EvidenceContext = append(aw.EvidenceContext, renderEvidenceLedgerPointerLine(key, operationID, runKind))
	if !shouldHydrateEvidenceForTurn(runKind, requestText, sess) {
		return aw
	}
	if err := ctx.Err(); err != nil {
		aw.EvidenceContext = append(aw.EvidenceContext, "evidence hydration skipped: "+err.Error())
		return aw
	}
	query := strings.TrimSpace(requestText)
	if operationID != "" {
		query = strings.TrimSpace(query + " operation_id:" + operationID)
	}
	result, err := r.store.HydrateEvidence(session.EvidenceHydrationQuery{
		Key:         key,
		OperationID: operationID,
		Query:       query,
		Limit:       maxRuntimeEvidenceContextLines,
		Now:         now,
	})
	if err != nil {
		aw.EvidenceContext = append(aw.EvidenceContext, "evidence hydration unavailable: "+compactEvidenceText(err.Error(), 180))
		return aw
	}
	aw.EvidenceContext = append(aw.EvidenceContext, renderEvidenceHydrationResultLines(result, runKind)...)
	return aw
}

func renderEvidenceLedgerPointerLine(key session.SessionKey, operationID string, runKind session.TurnRunKind) string {
	parts := []string{
		"evidence_ledger=available",
		"scope=" + session.SessionIDForKey(key),
		"run_kind=" + strings.TrimSpace(string(runKind)),
		"tool=evidence_hydrate",
	}
	if operationID = strings.TrimSpace(operationID); operationID != "" {
		parts = append(parts, "operation_id="+operationID)
	}
	return strings.Join(parts, " ")
}

func shouldHydrateEvidenceForTurn(runKind session.TurnRunKind, requestText string, sess *session.Session) bool {
	if runKind == session.TurnRunKindRecovery {
		return true
	}
	if sess == nil {
		return explicitEvidenceRecallRequest(requestText)
	}
	if continuationHydrationPressure(sess.ContinuationState) {
		return true
	}
	if operationHydrationPressure(sess.OperationState) {
		return true
	}
	return explicitEvidenceRecallRequest(requestText)
}

func continuationHydrationPressure(state session.ContinuationState) bool {
	state = session.NormalizeTurnAuthorizationState(state)
	switch state.Status {
	case session.TurnAuthorizationStatusPending, session.TurnAuthorizationStatusApproved:
		return true
	}
	switch state.ContinuationLease.Status {
	case session.ContinuationLeaseStatusActive, session.ContinuationLeaseStatusDeferred, session.ContinuationLeaseStatusConsumed:
		return true
	}
	switch state.ApprovalBundle.Status {
	case session.ContinuationLeaseStatusActive, session.ContinuationLeaseStatusDeferred, session.ContinuationLeaseStatusConsumed:
		return true
	}
	return strings.TrimSpace(state.ParkedReason) != "" || strings.TrimSpace(state.HandshakeBlockedReason) != ""
}

func operationHydrationPressure(state session.OperationState) bool {
	state = session.NormalizeOperationState(state)
	if !state.Active() {
		return false
	}
	switch state.Status {
	case session.OperationStatusActive, session.OperationStatusBlocked:
		return true
	default:
		return state.PhasePlan.Active() || state.PlanLease.Active()
	}
}

func explicitEvidenceRecallRequest(text string) bool {
	text = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if text == "" {
		return false
	}
	for _, needle := range []string{
		"where were we",
		"what were we doing",
		"recover context",
		"restore context",
		"prior context",
		"previous context",
		"what happened",
		"what changed",
		"show evidence",
		"from the evidence",
		"hydrate evidence",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	for _, term := range []string{"continue", "resume"} {
		if explicitEvidenceRecallContinuationTerm(text, term) {
			return true
		}
	}
	return false
}

func explicitEvidenceRecallContinuationTerm(text string, term string) bool {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_'
	})
	for i, word := range words {
		if word != term {
			continue
		}
		if term == "continue" {
			return true
		}
		if len(words) == 1 {
			return true
		}
		if i+1 < len(words) {
			next := words[i+1]
			if (next == "the" || next == "a" || next == "an") && i+2 < len(words) {
				next = words[i+2]
			}
			switch next {
			case "this", "that", "it", "work", "task", "thread", "context", "conversation", "operation", "goal", "phase":
				return true
			}
		}
		if i > 0 {
			switch words[i-1] {
			case "please", "now", "can", "could", "lets", "let", "to":
				return true
			}
		}
	}
	return false
}

func renderEvidenceHydrationResultLines(result session.EvidenceHydrationResult, runKind session.TurnRunKind) []string {
	lines := []string{
		fmt.Sprintf("hydration_run=%s run_kind=%s selected=%d missing=%d fallback=%t", result.RunID, runKind, len(result.Selected), len(result.MissingEvidenceIDs), result.FallbackUsed),
	}
	if len(result.MissingEvidenceIDs) > 0 {
		lines = append(lines, "missing_required_evidence="+strings.Join(result.MissingEvidenceIDs, "|"))
	}
	if result.FallbackUsed && strings.TrimSpace(result.FallbackReason) != "" {
		lines = append(lines, "fallback_reason="+compactEvidenceText(result.FallbackReason, 180))
	}
	for _, object := range result.Selected {
		parts := []string{
			"id=" + strings.TrimSpace(object.ID),
			"source=" + strings.TrimSpace(object.SourceKind),
			"status=" + strings.TrimSpace(object.EpistemicStatus),
		}
		if object.AuthorityClass != "" {
			parts = append(parts, "authority="+object.AuthorityClass)
		}
		if object.SubjectKey != "" {
			parts = append(parts, "subject="+compactEvidenceText(object.SubjectKey, 80))
		}
		if object.Summary != "" {
			parts = append(parts, "summary="+compactEvidenceText(object.Summary, 160))
		}
		if object.PayloadHash != "" {
			parts = append(parts, "hash="+compactEvidenceText(object.PayloadHash, 32))
		}
		lines = append(lines, strings.Join(parts, " "))
		if len(lines) >= maxRuntimeEvidenceContextLines+2 {
			break
		}
	}
	return lines
}

func compactEvidenceText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}
