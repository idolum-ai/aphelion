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
	if err := ctx.Err(); err != nil {
		aw.EvidenceContext = append(aw.EvidenceContext, "evidence hydration skipped: "+err.Error())
		return aw
	}
	operationID := strings.TrimSpace(sess.OperationState.ID)
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
