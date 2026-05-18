//go:build linux

package face

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func renderWatchdogHealthLine(health core.RestartHealthSnapshot) string {
	line := fmt.Sprintf(
		"watchdog enabled=%t triggered=%t stale_threshold=%s stale_limit=%d",
		health.WatchdogEnabled,
		health.WatchdogTriggered,
		health.StaleTurnThreshold,
		health.StaleTurnLimit,
	)
	if status := strings.TrimSpace(health.LastWatchdogStatus); status != "" {
		line += " last_status=" + status
	}
	if !health.LastWatchdogAt.IsZero() {
		line += " last_at=" + health.LastWatchdogAt.UTC().Format(time.RFC3339)
	}
	if !health.NextWatchdogAttemptAt.IsZero() {
		line += " next_at=" + health.NextWatchdogAttemptAt.UTC().Format(time.RFC3339)
	}
	if health.LastWatchdogStaleCount > 0 {
		line += fmt.Sprintf(" last_stale=%d", health.LastWatchdogStaleCount)
	}
	if health.LastWatchdogInterruptedCount > 0 {
		line += fmt.Sprintf(" last_interrupted=%d", health.LastWatchdogInterruptedCount)
	}
	if reason := strings.TrimSpace(health.LastWatchdogReason); reason != "" {
		line += " reason=" + strconv.Quote(reason)
	}
	return line
}
