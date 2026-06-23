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

const providerHealthWindow = 4 * time.Hour

func providerHealthFromExecutionEvents(events []session.ExecutionEvent, now time.Time) core.ProviderHealthSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	window := providerHealthWindow
	since := now.Add(-window)
	health := core.ProviderHealthSnapshot{
		GeneratedAt:  now,
		Window:       window,
		Status:       "healthy",
		StatusClass:  core.StatusClassCurrent,
		FailureClass: core.ReliabilityFailureNone,
		RetryPolicy:  core.ReliabilityRetryNone,
		NextAction:   "none",
	}
	for _, event := range events {
		eventType := strings.TrimSpace(event.EventType)
		if !providerHealthEventType(eventType) || event.CreatedAt.Before(since) {
			continue
		}
		if health.LastEventAt.IsZero() || event.CreatedAt.After(health.LastEventAt) {
			health.LastEventAt = event.CreatedAt
		}
		switch eventType {
		case core.ExecutionEventProviderAttemptFailed:
			health.RecentFailures++
			if health.LastFailureAt.IsZero() || event.CreatedAt.After(health.LastFailureAt) {
				payload := providerHealthPayload(event.PayloadJSON)
				health.LastFailureAt = event.CreatedAt
				health.LastFailureProvider = firstNonEmpty(providerHealthPayloadString(payload, "event_provider"), providerHealthPayloadString(payload, "provider"))
				health.LastFailureModel = providerHealthPayloadString(payload, "model")
				health.LastFailureError = trimError(firstNonEmpty(providerHealthPayloadString(payload, "error"), providerHealthPayloadString(payload, "reason")))
				health.LastFailureReason = firstNonEmpty(
					providerHealthPayloadString(payload, "failure_kind"),
					providerFailureOperatorReasonText(health.LastFailureError),
					providerHealthPayloadString(payload, "reason"),
					"provider_failure",
				)
			}
		case core.ExecutionEventProviderAttemptRetried:
			health.RecentRetries++
		case core.ExecutionEventProviderFailoverEngaged:
			health.RecentFailovers++
		case core.ExecutionEventProviderAttemptSucceeded:
			health.RecentSuccesses++
			if health.LastSuccessAt.IsZero() || event.CreatedAt.After(health.LastSuccessAt) {
				health.LastSuccessAt = event.CreatedAt
			}
		}
	}
	if !health.LastFailureAt.IsZero() {
		classification := core.ClassifyProviderReliability(health.LastFailureReason, health.LastFailureError)
		health.StatusClass = classification.StatusClass
		health.FailureClass = classification.FailureClass
		health.RetryPolicy = classification.RetryPolicy
		health.NextAction = classification.NextAction
	}
	if health.RecentFailures == 0 && health.RecentRetries == 0 && health.RecentFailovers == 0 {
		return health
	}
	if health.LastFailureAt.After(health.LastSuccessAt) || health.RecentSuccesses == 0 {
		health.Status = "degraded"
		return health
	}
	health.Status = "residual_risk"
	return health
}

func providerHealthEventType(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case core.ExecutionEventProviderAttemptFailed,
		core.ExecutionEventProviderAttemptRetried,
		core.ExecutionEventProviderFailoverEngaged,
		core.ExecutionEventProviderAttemptSucceeded:
		return true
	default:
		return false
	}
}

func providerHealthPayload(raw string) map[string]any {
	var payload map[string]any
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload
}

func providerHealthPayloadString(payload map[string]any, key string) string {
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

func (r *Runtime) writeDoctorProviderHealth(b *strings.Builder, now time.Time) {
	if r == nil || r.store == nil {
		doctor.WriteLine(b, "provider_health: unavailable")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	events, err := r.store.ExecutionEventsByTypes([]string{
		core.ExecutionEventProviderAttemptFailed,
		core.ExecutionEventProviderAttemptRetried,
		core.ExecutionEventProviderFailoverEngaged,
		core.ExecutionEventProviderAttemptSucceeded,
	}, now.Add(-providerHealthWindow), 200)
	if err != nil {
		doctor.WriteLine(b, "provider_health_error="+strconv.Quote(err.Error()))
		return
	}
	health := providerHealthFromExecutionEvents(events, now)
	doctor.WriteKV(b, "provider_health_status", health.Status)
	doctor.WriteKV(b, "provider_health_window", health.Window.Truncate(time.Second).String())
	doctor.WriteKV(b, "provider_health_failures", strconv.Itoa(health.RecentFailures))
	doctor.WriteKV(b, "provider_health_retries", strconv.Itoa(health.RecentRetries))
	doctor.WriteKV(b, "provider_health_failovers", strconv.Itoa(health.RecentFailovers))
	doctor.WriteKV(b, "provider_health_successes", strconv.Itoa(health.RecentSuccesses))
	doctor.WriteKV(b, "provider_health_status_class", health.StatusClass)
	doctor.WriteKV(b, "provider_health_failure_class", health.FailureClass)
	doctor.WriteKV(b, "provider_health_retry_policy", health.RetryPolicy)
	doctor.WriteKV(b, "provider_health_next_action", health.NextAction)
	if !health.LastFailureAt.IsZero() {
		doctor.WriteKV(b, "provider_health_last_failure_at", health.LastFailureAt.UTC().Format(time.RFC3339))
		doctor.WriteKV(b, "provider_health_last_failure_provider", health.LastFailureProvider)
		doctor.WriteKV(b, "provider_health_last_failure_model", health.LastFailureModel)
		doctor.WriteKV(b, "provider_health_last_failure_reason", health.LastFailureReason)
		doctor.WriteKV(b, "provider_health_last_failure_error", health.LastFailureError)
	}
	if !health.LastSuccessAt.IsZero() {
		doctor.WriteKV(b, "provider_health_last_success_at", health.LastSuccessAt.UTC().Format(time.RFC3339))
	}
}
