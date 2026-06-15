//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) evidenceHydrate(_ context.Context, input json.RawMessage, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("evidence_hydrate requires session store")
	}
	if key.ChatID == 0 && strings.TrimSpace(key.Scope.ID) == "" {
		return "", fmt.Errorf("evidence_hydrate requires current session scope")
	}
	var in evidenceHydrateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode evidence_hydrate input: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" && strings.TrimSpace(in.OperationID) == "" && len(in.RequiredEvidenceIDs) == 0 {
		return "", fmt.Errorf("evidence_hydrate requires query, operation_id, or required_evidence_ids")
	}
	result, err := r.store.HydrateEvidence(session.EvidenceHydrationQuery{
		Key:                 key,
		OperationID:         strings.TrimSpace(in.OperationID),
		Query:               strings.TrimSpace(in.Query),
		RequiredEvidenceIDs: in.RequiredEvidenceIDs,
		Limit:               in.Limit,
		Now:                 time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}
	return renderEvidenceHydrateResult(result), nil
}

func renderEvidenceHydrateResult(result session.EvidenceHydrationResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[EVIDENCE_HYDRATION]\n")
	fmt.Fprintf(&b, "run_id: %s\n", firstNonEmpty(result.RunID, "-"))
	fmt.Fprintf(&b, "session_id: %s\n", firstNonEmpty(result.SessionID, "-"))
	fmt.Fprintf(&b, "query: %s\n", firstNonEmpty(result.Query, "-"))
	fmt.Fprintf(&b, "selected: %d\n", len(result.Selected))
	if len(result.MissingEvidenceIDs) > 0 {
		fmt.Fprintf(&b, "missing_required: %s\n", strings.Join(result.MissingEvidenceIDs, ", "))
	}
	if result.FallbackUsed {
		fmt.Fprintf(&b, "fallback: %s\n", firstNonEmpty(result.FallbackReason, "used latest ledger snapshots"))
	}
	for i, obj := range result.Selected {
		fmt.Fprintf(&b, "\n%d. id: %s\n", i+1, obj.ID)
		fmt.Fprintf(&b, "   source: %s\n", firstNonEmpty(obj.SourceKind, "-"))
		fmt.Fprintf(&b, "   status: %s\n", firstNonEmpty(obj.EpistemicStatus, "-"))
		fmt.Fprintf(&b, "   subject: %s\n", firstNonEmpty(obj.SubjectKey, "-"))
		fmt.Fprintf(&b, "   hash: %s\n", firstNonEmpty(obj.PayloadHash, "-"))
		if strings.TrimSpace(obj.Summary) != "" {
			fmt.Fprintf(&b, "   summary: %s\n", clampToolOutputLine(obj.Summary, 240))
		}
		if strings.TrimSpace(obj.Digest) != "" {
			fmt.Fprintf(&b, "   digest: %s\n", clampToolOutputLine(obj.Digest, 500))
		}
	}
	b.WriteString("\n[/EVIDENCE_HYDRATION]")
	return b.String()
}

func clampToolOutputLine(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return strings.TrimSpace(value[:max-3]) + "..."
}
