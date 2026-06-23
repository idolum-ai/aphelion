//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/runtime/doctor"
	"github.com/idolum-ai/aphelion/session"
)

const persistenceHealthWindow = 4 * time.Hour

func persistenceHealthFromExecutionEvents(events []session.ExecutionEvent, now time.Time) core.PersistenceHealthSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	health := core.PersistenceHealthSnapshot{
		GeneratedAt:  now,
		Window:       persistenceHealthWindow,
		Status:       "healthy",
		StatusClass:  core.StatusClassCurrent,
		FailureClass: core.ReliabilityFailureNone,
		RetryPolicy:  core.ReliabilityRetryNone,
		NextAction:   "none",
	}
	since := now.Add(-persistenceHealthWindow)
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventPersistenceLatency || event.CreatedAt.Before(since) {
			continue
		}
		payload := persistenceLatencyPayload(event.PayloadJSON)
		latency := time.Duration(payloadLatencyMillis(payload)) * time.Millisecond
		classification := core.ClassifyPersistenceLatency(persistencePayloadString(payload, "component"), latency)
		if classification.FailureClass == core.ReliabilityFailureNone && strings.TrimSpace(event.Status) == "slow_write" {
			if latency <= 0 {
				latency = core.PersistenceLatencySlowThreshold
			}
			classification = core.ClassifyPersistenceLatency(persistencePayloadString(payload, "component"), latency)
		}
		if classification.FailureClass == core.ReliabilityFailureNone {
			continue
		}
		health.RecentSlow++
		if health.LastEventAt.IsZero() || event.CreatedAt.After(health.LastEventAt) {
			health.LastEventAt = event.CreatedAt
			health.LastComponent = firstNonEmptyPersistence(persistencePayloadString(payload, "component"), strings.TrimSpace(event.Stage), "persistence")
			health.LastLatency = latency
			health.StatusClass = classification.StatusClass
			health.FailureClass = classification.FailureClass
			health.RetryPolicy = classification.RetryPolicy
			health.NextAction = classification.NextAction
		}
	}
	if health.RecentSlow > 0 {
		health.Status = "degraded"
	}
	return health
}

func (r *Runtime) writeDoctorPersistenceHealth(b *strings.Builder, now time.Time) {
	if r == nil || r.store == nil {
		doctor.WriteLine(b, "persistence_health: unavailable")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	events, err := r.store.ExecutionEventsByTypes([]string{core.ExecutionEventPersistenceLatency}, now.Add(-persistenceHealthWindow), 200)
	if err != nil {
		doctor.WriteLine(b, "persistence_health_error="+strconv.Quote(err.Error()))
		return
	}
	health := persistenceHealthFromExecutionEvents(events, now)
	doctor.WriteKV(b, "persistence_health_status", health.Status)
	doctor.WriteKV(b, "persistence_health_window", health.Window.Truncate(time.Second).String())
	doctor.WriteKV(b, "persistence_health_slow_writes", strconv.Itoa(health.RecentSlow))
	doctor.WriteKV(b, "persistence_health_status_class", health.StatusClass)
	doctor.WriteKV(b, "persistence_health_failure_class", health.FailureClass)
	doctor.WriteKV(b, "persistence_health_retry_policy", health.RetryPolicy)
	doctor.WriteKV(b, "persistence_health_next_action", health.NextAction)
	if !health.LastEventAt.IsZero() {
		doctor.WriteKV(b, "persistence_health_last_at", health.LastEventAt.UTC().Format(time.RFC3339))
		doctor.WriteKV(b, "persistence_health_last_component", health.LastComponent)
		doctor.WriteKV(b, "persistence_health_last_latency_ms", strconv.FormatInt(health.LastLatency.Milliseconds(), 10))
	}
}

func persistenceLatencyPayload(raw string) map[string]any {
	var payload map[string]any
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload
}

func persistencePayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func payloadLatencyMillis(payload map[string]any) int64 {
	if payload == nil {
		return 0
	}
	value, ok := payload["latency_ms"]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func firstNonEmptyPersistence(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
