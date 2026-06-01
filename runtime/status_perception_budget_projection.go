//go:build linux

package runtime

import (
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const perceptionBudgetObservedEvidenceMarker = "observed_evidence_sources="

func latestPerceptionBudgetByChatFromExecutionEvents(events []session.ExecutionEvent) map[int64]core.PerceptionBudgetStatusSnapshot {
	out := make(map[int64]core.PerceptionBudgetStatusSnapshot)
	if len(events) == 0 {
		return out
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })
	for _, event := range ordered {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventProviderAttemptStarted {
			continue
		}
		projection, ok := perceptionBudgetStatusFromProviderAttempt(event)
		if !ok || projection.ChatID == 0 {
			continue
		}
		out[projection.ChatID] = projection
	}
	return out
}

func latestPerceptionBudgetForSessionFromExecutionEvents(events []session.ExecutionEvent, chatID int64) (core.PerceptionBudgetStatusSnapshot, bool) {
	if len(events) == 0 {
		return core.PerceptionBudgetStatusSnapshot{}, false
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })
	for i := len(ordered) - 1; i >= 0; i-- {
		event := ordered[i]
		if chatID != 0 && event.ChatID != chatID {
			continue
		}
		if strings.TrimSpace(event.EventType) != core.ExecutionEventProviderAttemptStarted {
			continue
		}
		projection, ok := perceptionBudgetStatusFromProviderAttempt(event)
		if ok {
			return projection, true
		}
	}
	return core.PerceptionBudgetStatusSnapshot{}, false
}

func perceptionBudgetStatusFromProviderAttempt(event session.ExecutionEvent) (core.PerceptionBudgetStatusSnapshot, bool) {
	payload := executionEventPayload(event.PayloadJSON)
	posture := strings.TrimSpace(payloadString(payload, "perception_posture"))
	if posture == "" {
		return core.PerceptionBudgetStatusSnapshot{}, false
	}
	projection := core.PerceptionBudgetStatusSnapshot{
		SessionID:               strings.TrimSpace(event.SessionID),
		ChatID:                  event.ChatID,
		ScopeKind:               strings.TrimSpace(string(event.Scope.Kind)),
		ScopeID:                 strings.TrimSpace(event.Scope.ID),
		AgentID:                 strings.TrimSpace(event.Scope.DurableAgentID),
		Seq:                     event.Seq,
		Posture:                 posture,
		AdmittedLayers:          payloadStringSlice(payload, "perception_admitted_layers"),
		SuppressedLayers:        payloadStringSlice(payload, "perception_suppressed_layers"),
		ObservedEvidenceSources: perceptionObservedEvidenceSources(payload),
		Risks:                   payloadStringSlice(payload, "perception_risks"),
		CreatedAt:               event.CreatedAt,
	}
	projection.TotalBudgetTokens, _ = payloadInt64(payload, "perception_total_budget_tokens")
	projection.TotalEstimatedTokens, _ = payloadInt64(payload, "perception_total_estimated_tokens")
	projection.MemoryBudgetTokens, _ = payloadInt64(payload, "perception_memory_budget_tokens")
	projection.MemoryEstimatedTokens, _ = payloadInt64(payload, "perception_memory_estimated_tokens")
	projection.CurrentInputTokens, _ = payloadInt64(payload, "perception_current_input_tokens")
	projection.ToolEvidenceTokens, _ = payloadInt64(payload, "perception_tool_evidence_tokens")
	projection.RemainingHeadroomTokens, _ = payloadInt64(payload, "perception_remaining_headroom_tokens")
	return projection, true
}

func perceptionObservedEvidenceSources(payload map[string]any) []string {
	explicit := payloadStringSlice(payload, "perception_observed_evidence_sources")
	if len(explicit) > 0 {
		return dedupeStatusStrings(explicit)
	}
	return dedupeStatusStrings(observedEvidenceSourcesFromLayerPayload(payloadStringSlice(payload, "perception_admitted_layers")))
}

func observedEvidenceSourcesFromLayerPayload(layers []string) []string {
	out := make([]string, 0, len(layers))
	for _, layer := range layers {
		layer = strings.TrimSpace(layer)
		if layer == "" {
			continue
		}
		if strings.HasPrefix(layer, perceptionBudgetObservedEvidenceMarker) {
			out = append(out, splitStatusCSV(strings.TrimPrefix(layer, perceptionBudgetObservedEvidenceMarker))...)
			continue
		}
		if strings.Contains(layer, "media.document_text_extraction") {
			out = append(out, "media.document_text_extraction")
		}
		if strings.Contains(layer, "floor_metadata.retained_artifact_context") {
			out = append(out, "floor_metadata.retained_artifact_context")
		}
	}
	return out
}

func dedupeStatusStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func summarizePerceptionBudgetStatus(snapshot core.PerceptionBudgetStatusSnapshot) string {
	parts := []string{"perception_posture=" + snapshot.Posture}
	if snapshot.RemainingHeadroomTokens != 0 || snapshot.TotalBudgetTokens != 0 {
		parts = append(parts, "headroom="+formatStatusInt64(snapshot.RemainingHeadroomTokens)+"/"+formatStatusInt64(snapshot.TotalBudgetTokens))
	}
	if snapshot.CurrentInputTokens != 0 {
		parts = append(parts, "current_input_tokens="+formatStatusInt64(snapshot.CurrentInputTokens))
	}
	if snapshot.ToolEvidenceTokens != 0 {
		parts = append(parts, "tool_evidence_tokens="+formatStatusInt64(snapshot.ToolEvidenceTokens))
	}
	if len(snapshot.AdmittedLayers) > 0 {
		parts = append(parts, "admitted_layers="+strings.Join(snapshot.AdmittedLayers, ","))
	}
	if len(snapshot.SuppressedLayers) > 0 {
		parts = append(parts, "suppressed_layers="+strings.Join(snapshot.SuppressedLayers, ","))
	}
	if len(snapshot.ObservedEvidenceSources) > 0 {
		parts = append(parts, "observed_evidence_sources="+strings.Join(snapshot.ObservedEvidenceSources, ","))
	}
	return strings.Join(parts, " ")
}

func formatStatusInt64(value int64) string {
	return strings.TrimSpace(payloadString(map[string]any{"value": value}, "value"))
}
