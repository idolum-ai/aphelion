//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestSystemStatusSnapshotProjectsProviderHealth(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7701, UserID: 0, Scope: telegramDMScopeRef(7701)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventProviderAttemptFailed,
			Stage:       "provider",
			Status:      "failed",
			PayloadJSON: `{"provider":"openrouter","model":"openrouter/sonnet","error":"context window exceeded"}`,
			CreatedAt:   now.Add(-2 * time.Minute),
		},
		{
			EventType:   core.ExecutionEventProviderAttemptRetried,
			Stage:       "provider",
			Status:      "retrying",
			PayloadJSON: `{"provider":"openrouter","model":"openrouter/sonnet","reason":"retryable transport failure"}`,
			CreatedAt:   now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(provider) err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if snapshot.ProviderHealth.Status != "degraded" {
		t.Fatalf("provider health = %#v, want degraded", snapshot.ProviderHealth)
	}
	if snapshot.ProviderHealth.RecentFailures != 1 || snapshot.ProviderHealth.RecentRetries != 1 {
		t.Fatalf("provider counts = failures %d retries %d, want 1/1", snapshot.ProviderHealth.RecentFailures, snapshot.ProviderHealth.RecentRetries)
	}
	if snapshot.ProviderHealth.LastFailureProvider != "openrouter" || !strings.Contains(snapshot.ProviderHealth.LastFailureError, "context window") {
		t.Fatalf("last provider failure = %#v, want openrouter context-window evidence", snapshot.ProviderHealth)
	}
	if snapshot.ProviderHealth.StatusClass != core.StatusClassOperationalTension ||
		snapshot.ProviderHealth.FailureClass != core.ReliabilityFailureProviderRequestShape ||
		snapshot.ProviderHealth.RetryPolicy != core.ReliabilityRetryRescopeRequest {
		t.Fatalf("provider classification = %#v, want operational request-shape rescope", snapshot.ProviderHealth)
	}
	if !strings.Contains(snapshot.ProviderHealth.NextAction, "rescope") {
		t.Fatalf("provider next action = %q, want rescope guidance", snapshot.ProviderHealth.NextAction)
	}
}

func TestDoctorProviderHealthIncludesRecentProviderPressure(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7702, UserID: 0, Scope: telegramDMScopeRef(7702)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventProviderAttemptFailed,
		Stage:       "provider",
		Status:      "failed",
		PayloadJSON: `{"provider":"openrouter","model":"openrouter/test","error":"insufficient_quota"}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(provider) err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorProviderHealth(&b, now)
	report := b.String()
	for _, want := range []string{
		`provider_health_status="degraded"`,
		`provider_health_failures="1"`,
		`provider_health_status_class="operational_tension"`,
		`provider_health_failure_class="provider_configuration"`,
		`provider_health_retry_policy="retry_after_config_repair"`,
		`provider_health_last_failure_provider="openrouter"`,
		`provider_health_last_failure_reason="quota exceeded"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("provider health report = %s, want %s", report, want)
		}
	}
}
