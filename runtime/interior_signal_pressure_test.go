//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestEvaluateHeartbeatInteriorSignalsRequiresThresholdAndCooldown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	states := []session.InteriorSignalState{
		{Category: hiddenInputSemanticRecurrence, SubjectKey: "release", Intensity: 0.59, ObservationCount: 2},
		{Category: hiddenInputUnresolvedMemory, SubjectKey: "release", Intensity: 0.25, ObservationCount: 1},
	}
	if got := evaluateHeartbeatInteriorSignals(states, now); got.Eligible {
		t.Fatalf("eligible below semantic threshold = %#v, want false", got)
	}

	states[0].Intensity = 0.65
	got := evaluateHeartbeatInteriorSignals(states, now)
	if !got.Eligible || len(got.Refs) != 2 {
		t.Fatalf("eligible crossed threshold = %#v, want true with refs", got)
	}

	states[0].CooldownUntil = now.Add(time.Hour)
	if got := evaluateHeartbeatInteriorSignals(states, now); got.Eligible {
		t.Fatalf("eligible during cooldown = %#v, want false", got)
	}
}
