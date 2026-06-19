//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	return renderEvidenceHydrateResult(result, evidenceHydrateRenderOptions{
		IncludePayloadIDs: in.IncludePayloadIDs,
		PayloadOffset:     in.PayloadOffset,
		PayloadLimit:      in.PayloadLimit,
	}), nil
}

const (
	defaultEvidenceHydratePayloadLimit = 4000
	maxEvidenceHydratePayloadLimit     = 12000
)

type evidenceHydrateRenderOptions struct {
	IncludePayloadIDs []string
	PayloadOffset     int
	PayloadLimit      int
}

func renderEvidenceHydrateResult(result session.EvidenceHydrationResult, opts evidenceHydrateRenderOptions) string {
	includePayload := normalizedEvidenceHydratePayloadIDSet(opts.IncludePayloadIDs)
	payloadOffset := opts.PayloadOffset
	if payloadOffset < 0 {
		payloadOffset = 0
	}
	payloadLimit := opts.PayloadLimit
	if payloadLimit <= 0 {
		payloadLimit = defaultEvidenceHydratePayloadLimit
	}
	if payloadLimit > maxEvidenceHydratePayloadLimit {
		payloadLimit = maxEvidenceHydratePayloadLimit
	}

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
		if _, ok := includePayload[obj.ID]; ok {
			renderEvidencePayloadWindow(&b, obj, payloadOffset, payloadLimit)
		}
	}
	b.WriteString("\n[/EVIDENCE_HYDRATION]")
	return b.String()
}

func normalizedEvidenceHydratePayloadIDSet(ids []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func renderEvidencePayloadWindow(b *strings.Builder, obj session.EvidenceObject, offset int, limit int) {
	if b == nil {
		return
	}
	if !session.EvidencePayloadHydrationAllowed(obj.RedactionClass) {
		fmt.Fprintf(b, "   payload_withheld: redaction_class=%s\n", firstNonEmpty(obj.RedactionClass, "unknown"))
		return
	}
	payload := strings.TrimSpace(obj.PayloadJSON)
	if payload == "" {
		payload = "{}"
	}
	content := evidencePayloadPreferredWindowContent(payload)
	redacted := session.RedactEvidenceText(content)
	renderRedactionClass := session.EvidenceRedactionClassForRedactions(redacted)
	if !session.EvidencePayloadHydrationAllowed(renderRedactionClass) {
		fmt.Fprintf(b, "   payload_withheld: redaction_class=%s\n", renderRedactionClass)
		return
	}
	content = redacted.Text
	window, nextOffset, truncated := evidencePayloadWindow(content, offset, limit)
	fmt.Fprintf(b, "   payload_window: offset=%d bytes=%d total_bytes=%d", offset, len(window), len(content))
	if truncated {
		fmt.Fprintf(b, " next_offset=%d", nextOffset)
	}
	fmt.Fprintf(b, "\n")
	if redacted.Redacted {
		fmt.Fprintf(b, "   payload_redaction: %s\n", session.EvidenceRedactionRedacted)
	}
	if window == "" {
		fmt.Fprintf(b, "   payload_text: <empty>\n")
		return
	}
	for _, line := range strings.Split(window, "\n") {
		fmt.Fprintf(b, "   payload_text: %s\n", line)
	}
}

func evidencePayloadPreferredWindowContent(payload string) string {
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return payload
	}
	if output, ok := decoded["output"].(string); ok {
		return output
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	normalized := map[string]any{}
	for _, key := range keys {
		normalized[key] = decoded[key]
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return payload
	}
	return string(raw)
}

func evidencePayloadWindow(content string, offset int, limit int) (string, int, bool) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultEvidenceHydratePayloadLimit
	}
	if limit > maxEvidenceHydratePayloadLimit {
		limit = maxEvidenceHydratePayloadLimit
	}
	if offset >= len(content) {
		return "", len(content), false
	}
	end := offset + limit
	if end >= len(content) {
		return strings.ToValidUTF8(content[offset:], "�"), len(content), false
	}
	return strings.ToValidUTF8(content[offset:end], "�"), end, true
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
