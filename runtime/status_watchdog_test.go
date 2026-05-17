//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestSystemStatusSnapshotIncludesLatestWatchdogEvent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
	createdAt := time.Now().UTC().Add(-time.Minute)
	nextAttemptAt := time.Now().UTC().Add(29 * time.Minute).Truncate(time.Second)
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventWatchdogRestartSuppressed,
		Stage:       "watchdog",
		Status:      "suppressed",
		PayloadJSON: `{"reason":"restart_cooldown_active","stale_count":2,"interrupted_count":0,"next_attempt_at":"` + nextAttemptAt.Format(time.RFC3339) + `"}`,
		CreatedAt:   createdAt,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if snapshot.RestartHealth.LastWatchdogStatus != "suppressed" ||
		snapshot.RestartHealth.LastWatchdogReason != "restart_cooldown_active" ||
		snapshot.RestartHealth.LastWatchdogStaleCount != 2 ||
		snapshot.RestartHealth.LastWatchdogInterruptedCount != 0 ||
		!snapshot.RestartHealth.NextWatchdogAttemptAt.Equal(nextAttemptAt) ||
		snapshot.RestartHealth.LastWatchdogAt.IsZero() {
		t.Fatalf("RestartHealth = %#v, want latest watchdog suppression details", snapshot.RestartHealth)
	}
}
