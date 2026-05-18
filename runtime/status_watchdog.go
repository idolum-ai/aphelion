//go:build linux

package runtime

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func restartHealthWithLatestWatchdogEvent(health core.RestartHealthSnapshot, events []session.ExecutionEvent) core.RestartHealthSnapshot {
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventWatchdogObserved,
			core.ExecutionEventWatchdogRecovered,
			core.ExecutionEventWatchdogRecoverySuppressed,
			core.ExecutionEventWatchdogFailed:
			health.LastWatchdogStatus = strings.TrimSpace(event.Status)
			if health.LastWatchdogStatus == "" {
				health.LastWatchdogStatus = strings.TrimPrefix(strings.TrimSpace(event.EventType), "watchdog.")
			}
			health.LastWatchdogAt = event.CreatedAt
			var payload map[string]any
			if strings.TrimSpace(event.PayloadJSON) != "" && json.Unmarshal([]byte(event.PayloadJSON), &payload) == nil {
				health.LastWatchdogReason = watchdogPayloadString(payload, "reason")
				if health.LastWatchdogReason == "" {
					health.LastWatchdogReason = watchdogPayloadString(payload, "error")
				}
				if nextAttemptAt, ok := watchdogPayloadTime(payload, "next_attempt_at"); ok {
					health.NextWatchdogAttemptAt = nextAttemptAt
				}
				health.LastWatchdogStaleCount = watchdogPayloadInt(payload, "stale_count")
				health.LastWatchdogInterruptedCount = watchdogPayloadInt(payload, "interrupted_count")
			}
			return health
		}
	}
	return health
}

func watchdogPayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func watchdogPayloadInt(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func watchdogPayloadTime(payload map[string]any, key string) (time.Time, bool) {
	raw := watchdogPayloadString(payload, key)
	if raw == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC(), true
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC(), true
	}
	return time.Time{}, false
}
