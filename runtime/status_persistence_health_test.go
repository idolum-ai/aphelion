//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestSystemStatusSnapshotProjectsPersistenceHealth(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7801, UserID: 0, Scope: telegramDMScopeRef(7801)}
	if err := store.RecordPersistenceLatencyClassification(key, "execution_events:mission_assessment", 300*time.Millisecond, time.Now().UTC()); err != nil {
		t.Fatalf("RecordPersistenceLatencyClassification() err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if snapshot.PersistenceHealth.Status != "degraded" || snapshot.PersistenceHealth.RecentSlow != 1 {
		t.Fatalf("persistence health = %#v, want one degraded slow-write classification", snapshot.PersistenceHealth)
	}
	if snapshot.PersistenceHealth.FailureClass != core.ReliabilityFailurePersistenceLatency ||
		snapshot.PersistenceHealth.RetryPolicy != core.ReliabilityRetryBatchBackpressure {
		t.Fatalf("persistence classification = %#v, want latency with batching/backpressure", snapshot.PersistenceHealth)
	}
	if !strings.Contains(snapshot.PersistenceHealth.NextAction, "batching") {
		t.Fatalf("persistence next action = %q, want batching guidance", snapshot.PersistenceHealth.NextAction)
	}
}

func TestSystemStatusSnapshotNormalPersistenceLatencyIsCurrentAndQuiet(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7803, UserID: 0, Scope: telegramDMScopeRef(7803)}
	if err := store.RecordPersistenceLatencyClassification(key, "execution_events:turn_started", core.PersistenceLatencySlowThreshold-time.Millisecond, time.Now().UTC()); err != nil {
		t.Fatalf("RecordPersistenceLatencyClassification() err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if snapshot.PersistenceHealth.Status != "healthy" || snapshot.PersistenceHealth.RecentSlow != 0 {
		t.Fatalf("persistence health = %#v, want healthy with no slow writes", snapshot.PersistenceHealth)
	}
	if snapshot.PersistenceHealth.StatusClass != core.StatusClassCurrent ||
		snapshot.PersistenceHealth.FailureClass != core.ReliabilityFailureNone ||
		snapshot.PersistenceHealth.RetryPolicy != core.ReliabilityRetryNone ||
		snapshot.PersistenceHealth.NextAction != "none" {
		t.Fatalf("persistence health classification = %#v, want quiet current classification", snapshot.PersistenceHealth)
	}
}

func TestDoctorPersistenceHealthIncludesSlowWriteClassification(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7802, UserID: 0, Scope: telegramDMScopeRef(7802)}
	now := time.Now().UTC()
	if err := store.RecordPersistenceLatencyClassification(key, "execution_events:background_recommendation", 300*time.Millisecond, now); err != nil {
		t.Fatalf("RecordPersistenceLatencyClassification() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorPersistenceHealth(&b, now.Add(time.Second))
	report := b.String()
	for _, want := range []string{
		`persistence_health_status="degraded"`,
		`persistence_health_slow_writes="1"`,
		`persistence_health_status_class="operational_tension"`,
		`persistence_health_failure_class="persistence_latency"`,
		`persistence_health_retry_policy="batch_or_backpressure"`,
		`persistence_health_last_component="execution_events:background_recommendation"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("persistence health report = %s, want %s", report, want)
		}
	}
}

func TestRuntimeAndDoctorPersistenceHealthUseTypedWindowUnderEventVolume(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7804, UserID: 0, Scope: telegramDMScopeRef(7804)}
	now := time.Now().UTC()
	if err := store.RecordPersistenceLatencyClassification(key, "execution_events:mission_assessment", 300*time.Millisecond, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("RecordPersistenceLatencyClassification() err = %v", err)
	}
	noisy := make([]session.ExecutionEventInput, 0, 600)
	for i := 0; i < 600; i++ {
		noisy = append(noisy, session.ExecutionEventInput{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-time.Minute).Add(time.Duration(i) * time.Millisecond),
		})
	}
	if _, err := store.AppendExecutionEvents(key, noisy); err != nil {
		t.Fatalf("AppendExecutionEvents(noisy) err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if snapshot.PersistenceHealth.Status != "degraded" || snapshot.PersistenceHealth.RecentSlow != 1 {
		t.Fatalf("persistence health = %#v, want degraded with one slow write despite noisy events", snapshot.PersistenceHealth)
	}
	if snapshot.PersistenceHealth.FailureClass != core.ReliabilityFailurePersistenceLatency ||
		snapshot.PersistenceHealth.RetryPolicy != core.ReliabilityRetryBatchBackpressure {
		t.Fatalf("persistence classification = %#v, want latency/backpressure classification", snapshot.PersistenceHealth)
	}

	var b strings.Builder
	rt.writeDoctorPersistenceHealth(&b, now)
	report := b.String()
	for _, want := range []string{
		`persistence_health_status="degraded"`,
		`persistence_health_slow_writes="1"`,
		`persistence_health_status_class="` + snapshot.PersistenceHealth.StatusClass + `"`,
		`persistence_health_failure_class="` + snapshot.PersistenceHealth.FailureClass + `"`,
		`persistence_health_retry_policy="` + snapshot.PersistenceHealth.RetryPolicy + `"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("persistence health report = %s, want %s", report, want)
		}
	}
}
