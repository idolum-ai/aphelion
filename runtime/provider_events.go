//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) recordProviderAttemptEvents(key session.SessionKey, exec pipeline.TurnExecutionContract, result *core.TurnResult) {
	if r == nil || result == nil || len(result.ProviderEvents) == 0 {
		return
	}
	for _, event := range result.ProviderEvents {
		eventType := strings.TrimSpace(event.EventType)
		if eventType == "" {
			continue
		}
		status := providerAttemptEventStatus(eventType)
		payload := map[string]any{
			"backend":       strings.TrimSpace(exec.Backend),
			"provider":      strings.TrimSpace(exec.ProviderName),
			"model":         strings.TrimSpace(exec.ModelName),
			"provider_path": strings.Join(exec.ProviderPath, ","),
		}
		if value := strings.TrimSpace(event.Provider); value != "" {
			payload["event_provider"] = value
		}
		if value := strings.TrimSpace(event.FromProvider); value != "" {
			payload["from_provider"] = value
		}
		if value := strings.TrimSpace(event.ToProvider); value != "" {
			payload["to_provider"] = value
		}
		if event.Attempt > 0 {
			payload["attempt"] = event.Attempt
		}
		if event.MaxRetries > 0 {
			payload["max_retries"] = event.MaxRetries
		}
		if value := strings.TrimSpace(event.Error); value != "" {
			payload["error"] = trimError(value)
		}
		if value := strings.TrimSpace(event.FailureKind); value != "" {
			payload["failure_kind"] = value
		}
		payload["retryable"] = event.Retryable
		payload["failover_eligible"] = event.FailoverEligible
		if value := strings.TrimSpace(event.Reason); value != "" {
			payload["reason"] = value
		}
		if value := strings.TrimSpace(event.ResponseID); value != "" {
			payload["response_id"] = value
		}
		if event.PartialContentChars > 0 {
			payload["partial_content_chars"] = event.PartialContentChars
		}
		if event.PartialToolCalls > 0 {
			payload["partial_tool_calls"] = event.PartialToolCalls
		}
		appendTokenUsagePayload(payload, result.TokenUsage)
		observedAt := event.ObservedAt
		if observedAt.IsZero() {
			observedAt = time.Now().UTC()
		}
		r.recordExecutionEvent(key, eventType, "provider", status, payload, observedAt.UTC())
	}
}

func (r *Runtime) warnProviderFailovers(ctx context.Context, key session.SessionKey, events []core.ProviderEvent) {
	if r == nil || r.outbound == nil || key.ChatID == 0 || len(events) == 0 {
		return
	}
	seen := map[string]struct{}{}
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventProviderFailoverEngaged {
			continue
		}
		from := firstRuntimeWorkNonEmpty(event.FromProvider, event.Provider, "primary provider")
		to := firstRuntimeWorkNonEmpty(event.ToProvider, "next provider")
		line := providerFailoverWarningLine(from, to, event)
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: key.ChatID, Text: line}); err != nil {
			return
		}
	}
}

func providerFailoverWarningLine(from string, to string, event core.ProviderEvent) string {
	from = firstRuntimeWorkNonEmpty(from, event.FromProvider, event.Provider, "primary provider")
	to = firstRuntimeWorkNonEmpty(to, event.ToProvider, "next provider")
	if reason := providerFailureOperatorReason(event); reason != "" {
		return fmt.Sprintf("Provider fallback: %s %s; trying %s.", from, reason, to)
	}
	return fmt.Sprintf("Provider fallback: %s failed; trying %s.", from, to)
}

func providerFailureOperatorReason(event core.ProviderEvent) string {
	for _, value := range []string{event.Reason, event.Error} {
		reason := providerFailureOperatorReasonText(value)
		if reason != "" {
			return reason
		}
	}
	return ""
}

func providerFailureOperatorReasonText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	switch {
	case strings.Contains(value, "insufficient_quota") ||
		strings.Contains(value, "exceeded your current quota") ||
		strings.Contains(value, "billing details") ||
		strings.Contains(value, "quota exceeded"):
		return "quota exceeded"
	default:
		return ""
	}
}

func providerAttemptEventStatus(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case core.ExecutionEventProviderAttemptRetried:
		return "retrying"
	case core.ExecutionEventProviderAttemptFailed:
		return "failed"
	case core.ExecutionEventProviderFailoverEngaged:
		return "engaged"
	case core.ExecutionEventProviderPartial:
		return "partial"
	default:
		return "observed"
	}
}

func providerNameAfterProviderEvents(defaultName string, events []core.ProviderEvent) string {
	name := strings.TrimSpace(defaultName)
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventProviderFailoverEngaged:
			if to := strings.TrimSpace(event.ToProvider); to != "" {
				name = to
			}
		case core.ExecutionEventProviderAttemptSucceeded:
			if provider := strings.TrimSpace(event.Provider); provider != "" {
				name = provider
			}
		}
	}
	return name
}
