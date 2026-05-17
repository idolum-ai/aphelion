//go:build linux

package face

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRenderTelegramStatusSystemIncludesWatchdogDetails(t *testing.T) {
	t.Parallel()

	out := RenderTelegramStatusSystem(core.SystemStatusSnapshot{
		RestartHealth: core.RestartHealthSnapshot{
			WatchdogEnabled:              true,
			WatchdogTriggered:            true,
			StaleTurnThreshold:           3 * time.Minute,
			StaleTurnLimit:               8,
			WatchdogRestartCooldown:      30 * time.Minute,
			WatchdogMaxRestartAttempts:   1,
			LastWatchdogStatus:           "suppressed",
			LastWatchdogReason:           "restart_cooldown_active",
			LastWatchdogAt:               time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
			NextWatchdogAttemptAt:        time.Date(2026, 5, 17, 12, 30, 0, 0, time.UTC),
			LastWatchdogStaleCount:       2,
			LastWatchdogInterruptedCount: 1,
		},
	}, "medium", "high")

	for _, needle := range []string{
		"watchdog enabled=true triggered=true stale_threshold=3m0s stale_limit=8 restart_cooldown=30m0s max_restart_attempts=1",
		"last_status=suppressed",
		"last_at=2026-05-17T12:00:00Z",
		"next_at=2026-05-17T12:30:00Z",
		"last_stale=2",
		"last_interrupted=1",
		`reason="restart_cooldown_active"`,
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderTelegramStatusSystem() = %q, want %q", out, needle)
		}
	}
}
