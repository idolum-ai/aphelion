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
		r.recordExecutionEvent(key, eventType, "provider", status, payload, time.Now().UTC())
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
		line := fmt.Sprintf("Provider fallback: %s failed; trying %s.", from, to)
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: key.ChatID, Text: line}); err != nil {
			return
		}
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
	default:
		return "observed"
	}
}
